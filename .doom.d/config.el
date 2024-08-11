;;; $DOOMDIR/config.el -*- lexical-binding: t; -*-

;; Place your private configuration here! Remember, you do not need to run 'doom
;; sync' after modifying this file!

;; reload with M-x eval-buffer RET


;; Some functionality uses this to identify you, e.g. GPG configuration, email
;; clients, file templates and snippets.
(setq user-full-name "Carl Helmertz"
      user-mail-address "helmertz@gmail.com")

;; Doom exposes five (optional) variables for controlling fonts in Doom. Here
;; are the three important ones:
;;
;; + `doom-font'
;; + `doom-variable-pitch-font'
;; + `doom-big-font' -- used for `doom-big-font-mode'; use this for
;;   presentations or streaming.
;;
;; They all accept either a font-spec, font string ("Input Mono-12"), or xlfd
;; font string. You generally only need these two:
(setq doom-font (font-spec :family "monospace"))

;; There are two ways to load a theme. Both assume the theme is installed and
;; available. You can either set `doom-theme' or manually load a theme with the
;; `load-theme' function. This is the default:
(setq doom-theme 'doom-one) ; dark, default
;;(setq doom-theme 'doom-acario-light) ; light
;; this is overridden by auto-dark-mode

;; If you use `org' and don't want your org files in the default location below,
;; change `org-directory'. It must be set before org loads!
(setq org-directory "~/Dropbox/orgzly/")

;; This determines the style of line numbers in effect. If set to `nil', line
;; numbers are disabled. For relative line numbers, set this to `relative'.
(setq display-line-numbers-type t)


;; Here are some additional functions/macros that could help you configure Doom:
;;
;; - `load!' for loading external *.el files relative to this one
;; - `use-package' for configuring packages
;; - `after!' for running code after a package has loaded
;; - `add-load-path!' for adding directories to the `load-path', relative to
;;   this file. Emacs searches the `load-path' when you load packages with
;;   `require' or `use-package'.
;; - `map!' for binding new keys
;;
;; To get information about any of these functions/macros, move the cursor over
;; the highlighted symbol at press 'K' (non-evil users must press 'C-c g k').
;; This will open documentation for it, including demos of how they are used.
;;
;; You can also try 'gd' (or 'C-c g d') to jump to their definition and see how
;; they are implemented.


;; from installation notes:
;;
;;1. Whenever you edit your doom! block in ~/.doom.d/init.el or modify your
;;   modules, run:
;;
;;     bin/doom refresh
;;
;;   This will ensure all needed packages are installed, all orphaned packages are
;;   removed, and your autoloads files are up to date. This is important! If you
;;   forget to do this you will get errors!
;;
;;2. If something inexplicably goes wrong, try `bin/doom doctor`
;;
;;   This will diagnose common issues with your environment and setup, and may
;;   give you clues about what is wrong.
;;
;;3. Use `bin/doom upgrade` to update Doom. Doing it any other way may require
;;   additional work. When in doubt, run `bin/doom sync`.
;;
;;4. Check out `bin/doom help` to see what else `bin/doom` can do (and it is
;;   recommended you add ~/.emacs.d/bin to your PATH).
;;
;;5. You can find Doom's documentation via `M-x doom/help` or `SPC h D`.

;; doom bindings:
;; list buffers: ,<
;; org capture: ,X

(require 'iso-transl)

(add-to-list 'default-frame-alist '(fullscreen . maximized))
(setq confirm-kill-emacs nil)
(after! org
  (setq org-adapt-indentation nil
        org-link-descriptive nil
        org-html-doctype "html5"
        org-return-follows-link t
        org-startup-indented nil
        calendar-week-start-day 1
        org-agenda-start-on-weekday 1
        org-export-with-sub-superscripts nil
        org-log-into-drawer t ; this logs to :LOGBOOK: by default
        org-todo-keywords '((sequence "TODO(!)" "PROGRESS(!)" "|" "DONE(!)" "WONT(!)"))
        org-capture-templates '(
                                ("t" "TODO" entry (file "inbox.org") "* TODO %?\n:PROPERTIES:\n:CREATED: %T\n:END:\n\n  %i" :empty-lines-after 1)
                                ("j" "Journal" entry (file+olp+datetree "journal.org") "* %?\n:PROPERTIES:\n:CREATED: %T\n:END:\n" :jump-to-captured t :empty-lines-after 1)
                                ("m" "MATCHi DS" entry (file+olp+datetree "matchi.org" "DS") "* %?\n:PROPERTIES:\n:CREATED: %U\n:END:\n%i" :jump-to-captured t :empty-lines 1)
                                ("b" "Bug" entry (file+olp+datetree "bugs.org") "* %?\n:PROPERTIES:\n:CREATED: %U\n:END:\n%i" :jump-to-captured t :empty-lines 1)
                                )
        ))
(remove-hook 'doom-first-input-hook #'evil-snipe-mode)

(after! doom-ui
  (setq! auto-dark-dark-theme 'doom-one)
  (setq! auto-dark-light-theme 'doom-acario-light)
  (auto-dark-mode 1))

(defun export-html-and-open ()
  (interactive)
  (if (derived-mode-p 'org-mode)
      (org-open-file (org-html-export-to-html) 'system)
    (browse-url-of-buffer))
  (shell-command "wmctrl -a firefox"))

;; from https://github.com/abo-abo/oremacs/commit/6c86696c0a1f66bf690e1a934683f85f04c6f34d
;; but slightly modified (to txt instead of html)
(defun org-to-plaintext-to-clipboard ()
  "Export region to HTML, and copy it to the clipboard."
  (interactive)
  (org-export-to-file 'ascii "/tmp/org.txt")
  (apply
   'start-process "xclip" "*xclip*"
   (split-string
    "xclip -verbose -i /tmp/org.txt -t text/plain -selection clipboard" " ")))

(defun my-comment-dwim ()
  (interactive)
  (if (use-region-p)
      (comment-or-uncomment-region (region-beginning) (region-end))
    (comment-or-uncomment-region (line-beginning-position) (line-end-position))))


(setq doom-leader-key ","
      doom-localleader-key ",")
(map! "<f6>" 'projectile-replace)
(map! :leader "e" (lambda() (interactive) (find-file (concat org-directory "matchi.org"))))
(map! :leader "j" (lambda() (interactive) (find-file (concat org-directory "journal.org"))))
(map! :leader "w" 'kill-buffer-and-window)
(map! :leader "m" '(lambda() (interactive) (org-capture nil "j")))
(map! :leader "a" 'org-agenda)
(map! :leader "(" 'my-comment-dwim)
(map! :map org-mode-map :leader "h" 'export-html-and-open)
(map! :map org-mode-map :leader "t" 'org-to-plaintext-to-clipboard)
(map! :map org-mode-map :leader "c i" 'org-clock-in)
(map! :map org-mode-map :leader "c o" 'org-clock-out)
(map! :leader "+" 'doom/increase-font-size)
(map! :leader "-" 'doom/decrease-font-size)
(map! :leader "." '+default/search-project)


;; see https://github.com/hlissner/doom-emacs/issues/3172
(add-hook 'org-mode-hook (lambda () (electric-indent-local-mode -1)))


;; see https://joy.pm/post/2017-09-17-a_graphviz_primer/
(defun my/fix-inline-images ()
  (when org-inline-image-overlays
    (org-redisplay-inline-images)))

(add-hook 'org-babel-after-execute-hook 'my/fix-inline-images)

;; at work, Dockerfile-x is a common naming scheme
(add-to-list 'auto-mode-alist '("Dockerfile.*" . dockerfile-mode))
