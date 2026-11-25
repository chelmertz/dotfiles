"""Tests for claude-security-gate-hook. Run: python3 bin/claude-security-gate-hook_test.py

No CI job runs these; they run by hand when the hook changes.
"""
import contextlib
import importlib.machinery
import importlib.util
import io
import os
import subprocess
import tempfile
import time
import unittest

HERE = os.path.dirname(os.path.abspath(__file__))
_loader = importlib.machinery.SourceFileLoader("gate", os.path.join(HERE, "claude-security-gate-hook"))
_spec = importlib.util.spec_from_loader("gate", _loader)
gate = importlib.util.module_from_spec(_spec)
_loader.exec_module(gate)


def sh(repo, *args):
    subprocess.run(["git", "-C", repo, *args], check=True, capture_output=True,
                   env={**os.environ, "GIT_AUTHOR_NAME": "t", "GIT_AUTHOR_EMAIL": "t@t",
                        "GIT_COMMITTER_NAME": "t", "GIT_COMMITTER_EMAIL": "t@t"})


class Gate(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.mkdtemp()
        # ~/p/<ns>/<project>/<clone>, with the p root moved into the temp dir.
        self.proot = os.path.join(self.tmp, "p")
        self.project = os.path.join(self.proot, "m", "proj")
        self.repo = os.path.join(self.project, "clone")
        os.makedirs(self.repo)
        os.environ["CLAUDE_P_ROOT"] = self.proot
        sh(self.repo, "init", "-q", "-b", "main")
        self.write("README.md", "x")
        sh(self.repo, "add", "-A")
        sh(self.repo, "commit", "-q", "-m", "base")
        sh(self.repo, "update-ref", "refs/remotes/origin/main", "HEAD")
        sh(self.repo, "checkout", "-q", "-b", "feat")
        self.write("main.go", "package main")
        sh(self.repo, "add", "-A")
        sh(self.repo, "commit", "-q", "-m", "code")

    def tearDown(self):
        os.environ.pop("CLAUDE_P_ROOT", None)

    def write(self, name, text):
        with open(os.path.join(self.repo, name), "w") as f:
            f.write(text)

    def pr_create(self):
        """Exit code and stderr of the gate on `gh pr create` from the clone."""
        err = io.StringIO()
        payload = {"tool_name": "Bash", "hook_event_name": "PreToolUse", "cwd": self.repo,
                   "tool_input": {"command": "gh pr create --draft --title t"}}
        with contextlib.redirect_stderr(err):
            try:
                gate.gate(payload)
            except SystemExit as e:
                return e.code, err.getvalue()
        return 0, err.getvalue()

    def skill(self, name):
        gate.record({"tool_name": "Skill", "hook_event_name": "PostToolUse", "cwd": self.repo,
                     "tool_input": {"skill": name, "args": ""}})

    def ran(self, command):
        gate.record_tests({"tool_name": "Bash", "hook_event_name": "PostToolUse", "cwd": self.repo,
                           "tool_input": {"command": command}})

    def test_a_code_branch_needs_both_reviews_and_tests_at_its_head(self):
        code, err = self.pr_create()
        self.assertEqual(2, code)
        for missing in ("security-review", "code-review", "tests"):
            self.assertIn(missing, err)

        self.skill("security-review")
        self.skill("code-review")
        code, err = self.pr_create()
        self.assertEqual(2, code)
        self.assertIn("tests", err)
        self.assertNotIn("code-review", err.split("Missing:")[1].split("\n")[0])

        os.makedirs(os.path.join(self.repo, "domains", "x"))
        self.ran("cd domains/x && CGO_ENABLED=0 capped go test ./... -count=1")
        self.assertEqual(0, self.pr_create()[0])

    def test_a_commit_after_the_tests_needs_them_again_but_not_the_reviews(self):
        self.skill("security-review")
        self.skill("code-review")
        self.ran("capped go test ./...")
        self.write("fix.go", "package main")
        sh(self.repo, "add", "-A")
        sh(self.repo, "commit", "-q", "-m", "fix")
        code, err = self.pr_create()
        self.assertEqual(2, code)
        self.assertIn("tests", err)
        self.assertNotIn("security-review", err)

    def test_a_filtered_test_run_does_not_count(self):
        self.skill("security-review")
        self.skill("code-review")
        for filtered in ("go test ./cmd/x -run TestOne", "go test ./... -run 'A|B'", "npx vitest run src/a.test.ts"):
            self.ran(filtered)
            self.assertEqual(2, self.pr_create()[0], filtered)

    def test_a_design_note_newer_than_the_branch_needs_the_persona_review(self):
        self.skill("security-review")
        self.skill("code-review")
        self.ran("go test ./...")
        note = os.path.join(self.project, "thing-design.md")
        with open(note, "w") as f:
            f.write("design")
        later = time.time() + 60
        os.utime(note, (later, later))
        code, err = self.pr_create()
        self.assertEqual(2, code)
        self.assertIn("persona-review", err)
        self.assertIn("thing-design.md", err)
        self.skill("persona-review")
        self.assertEqual(0, self.pr_create()[0])

    def test_a_design_note_older_than_the_branch_is_not_asked_about(self):
        note = os.path.join(self.project, "old-design.md")
        with open(note, "w") as f:
            f.write("design")
        os.utime(note, (1, 1))
        self.skill("security-review")
        self.skill("code-review")
        self.ran("go test ./...")
        self.assertEqual(0, self.pr_create()[0])

    def test_a_docs_only_branch_still_needs_only_the_persona_review(self):
        sh(self.repo, "checkout", "-q", "main")
        sh(self.repo, "checkout", "-q", "-b", "docs")
        self.write("spec.md", "y")
        sh(self.repo, "add", "-A")
        sh(self.repo, "commit", "-q", "-m", "docs")
        self.assertEqual(2, self.pr_create()[0])
        self.skill("persona-review")
        self.assertEqual(0, self.pr_create()[0])


if __name__ == "__main__":
    unittest.main()
