package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"strings"
)

// Proxy is the only path from an agent's jail to Anthropic. It serves only
// peers that are a registered run's agent unit, forwards only allowlisted
// message requests, swaps the agent's placeholder key for the seat token,
// and charges every token to the run's org and command.
type Proxy struct {
	Upstream  *url.URL
	Token     func() (string, error)
	Registry  string // directory of <run>.json registrations
	Accounts  *Accounts
	PeerGroup func(net.Conn) (string, error) // cgroup of the connecting process
	Transport http.RoundTripper
}

type ctxKey int

const peerKey ctxKey = 0

type peerInfo struct {
	cgroup string
	err    error
}

// ConnContext identifies the peer once per connection, right after accept,
// before any byte from it is read.
func (p *Proxy) ConnContext(ctx context.Context, c net.Conn) context.Context {
	cg, err := p.PeerGroup(c)
	return context.WithValue(ctx, peerKey, peerInfo{cg, err})
}

// refusalStatus is what every proxy refusal answers with. Claude Code's client
// retries 408, 409, 429 and 5xx; a 403 ends the request, so a refusal ends the
// run instead of looping on the budget or a bad request.
const refusalStatus = http.StatusForbidden

func (p *Proxy) deny(w http.ResponseWriter, rr *Registration, r *refusal) {
	run := "-"
	if rr != nil {
		run = rr.Run
		if err := p.Accounts.Refused(*rr, r.Reason); err != nil {
			log.Printf("run=%s recording refusal: %v", run, err)
		}
	}
	log.Printf("refused run=%s reason=%s detail=%s", run, r.Reason, r.Detail)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(refusalStatus)
	json.NewEncoder(w).Encode(map[string]any{
		"type":  "error",
		"error": map[string]string{"type": "permission_error", "message": "wiki-proxy refused: " + r.Reason},
	})
}

func (p *Proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	peer, _ := r.Context().Value(peerKey).(peerInfo)
	if peer.err != nil {
		p.deny(w, nil, refuse("peer", "%v", peer.err))
		return
	}
	run := runFromCgroup(peer.cgroup)
	if run == "" {
		p.deny(w, nil, refuse("peer", "cgroup %q is not an agent unit", peer.cgroup))
		return
	}
	rr, err := lookupRun(p.Registry, run)
	if err != nil {
		p.deny(w, nil, refuse("unregistered", "run %s: %v", run, err))
		return
	}
	betas, ref := checkRequest(r)
	if ref != nil {
		p.deny(w, &rr, ref)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, maxBody+1))
	if err != nil || len(body) > maxBody {
		p.deny(w, &rr, refuse("body", "unreadable or over %d bytes", maxBody))
		return
	}
	info, ref := checkBody(body)
	if ref != nil {
		p.deny(w, &rr, ref)
		return
	}
	token, err := p.Token()
	if err != nil || token == "" {
		p.deny(w, &rr, refuse("token", "%v", err))
		return
	}
	// count_tokens is free; a message request reserves its worst case: the
	// whole body as input (bytes/2 overestimates tokens for text and JSON)
	// plus every output token it may generate.
	var reserved int64
	if r.URL.Path == "/v1/messages" {
		reserved = int64(len(body))/2 + info.MaxTokens
		if ref := p.Accounts.Reserve(rr, info.Model, reserved); ref != nil {
			p.deny(w, &rr, ref)
			return
		}
	}
	settle := func(rep *Reported) {
		if reserved == 0 {
			return
		}
		if err := p.Accounts.Settle(rr, reserved, rep); err != nil {
			log.Printf("run=%s settling: %v", rr.Run, err)
		}
	}

	out := r.Clone(r.Context())
	out.Body = io.NopCloser(bytes.NewReader(body))
	out.ContentLength = int64(len(body))
	out.Header = http.Header{}
	for k, v := range r.Header {
		if allowedHeaders[k] && k != "Anthropic-Beta" {
			out.Header[k] = v
		}
	}
	out.Header.Set("Authorization", "Bearer "+token)
	out.Header.Set("Anthropic-Beta", strings.Join(append(betas, oauthBeta), ","))

	rp := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(p.Upstream)
			pr.Out.Host = p.Upstream.Host
			if pr.In.URL.RawQuery == "beta=true" {
				pr.Out.URL.RawQuery = "beta=true"
			} else {
				pr.Out.URL.RawQuery = ""
			}
		},
		Transport:     p.Transport,
		FlushInterval: -1,
		ModifyResponse: func(resp *http.Response) error {
			if resp.StatusCode < 200 || resp.StatusCode > 299 {
				// Anthropic does not bill a request it answered with an error.
				settle(&Reported{})
				return nil
			}
			resp.Body = newUsageTap(resp.Body, resp.Header.Get("Content-Type"), settle)
			return nil
		},
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, err error) {
			// Never reached Anthropic, or failed mid-way: keep the charge.
			log.Printf("run=%s upstream: %v", rr.Run, err)
			w.WriteHeader(http.StatusBadGateway)
		},
	}
	rp.ServeHTTP(w, out)
}

// readToken reads the seat token from a systemd credential file.
func readToken(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	t := strings.TrimSpace(string(b))
	if t == "" || strings.ContainsAny(t, " \t\r\n") {
		return "", fmt.Errorf("%s does not hold one token", path)
	}
	return t, nil
}
