;;; $DOOMDIR/config.el -*- lexical-binding: t; -*-

;; Place your private configuration here! Remember, you do not need to run 'doom
;; sync' after modifying this file!


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
(setq doom-font (font-spec :family "monospace" :size 14))

;; There are two ways to load a theme. Both assume the theme is installed and
;; available. You can either set `doom-theme' or manually load a theme with the
;; `load-theme' function. This is the default:
(setq doom-theme 'doom-one)

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


; from installation notes:
;
;1. Whenever you edit your doom! block in ~/.doom.d/init.el or modify your
;   modules, run:
;
;     bin/doom refresh
;
;   This will ensure all needed packages are installed, all orphaned packages are
;   removed, and your autoloads files are up to date. This is important! If you
;   forget to do this you will get errors!
;
;2. If something inexplicably goes wrong, try `bin/doom doctor`
;
;   This will diagnose common issues with your environment and setup, and may
;   give you clues about what is wrong.
;
;3. Use `bin/doom upgrade` to update Doom. Doing it any other way may require
;   additional work. When in doubt, run `bin/doom sync`.
;
;4. Check out `bin/doom help` to see what else `bin/doom` can do (and it is
;   recommended you add ~/.emacs.d/bin to your PATH).
;
;5. You can find Doom's documentation via `M-x doom/help` or `SPC h D`.

; doom bindings:
; list buffers: ,<
; org capture: ,X


(add-to-list 'default-frame-alist '(fullscreen . maximized))
(setq confirm-kill-emacs nil)
(after! org
  (setq org-adapt-indentation nil
        org-startup-indented nil
        org-export-with-sub-superscripts nil
        org-capture-templates '(
                                ("t" "TODO" entry (file "inbox.org") "* TODO %?\n:PROPERTIES:\n:CREATED: %T\n:END:\n\n  %i")
                                ("j" "Journal" entry (file+datetree "journal.org") "* %?\n:PROPERTIES:\n:CREATED: %T\n:END:\n" :jump-to-captured t)
                                ("e" "Elvaco TODO" entry (file+headline "elvaco.org" "TODOs")
                                 "* TODO %?\n:PROPERTIES:\n:CREATED: %T\n:END:\n\n  %i")
                                ("r" "Elvaco retrospective" entry (file+headline "elvaco.org" "Retrospective")
                                 "* TODO %?\n:PROPERTIES:\n:CREATED: %T\n:END:\n\n  %i")
                                )
        ))

(defun export-html-and-open ()
  (interactive)
  (org-open-file (org-html-export-to-html)))

; from https://github.com/abo-abo/oremacs/commit/6c86696c0a1f66bf690e1a934683f85f04c6f34d
; but slightly modified (to txt instead of html)
(defun org-to-plaintext-to-clipboard ()
  "Export region to HTML, and copy it to the clipboard."
  (interactive)
  (org-export-to-file 'ascii "/tmp/org.txt")
  (apply
   'start-process "xclip" "*xclip*"
   (split-string
    "xclip -verbose -i /tmp/org.txt -t text/plain -selection clipboard" " ")))


(setq doom-leader-key ",")
(map! "<f6>" 'projectile-replace)
(map! :leader "e" (lambda() (interactive) (find-file (concat org-directory "elvaco.org"))))
(map! :leader "j" (lambda() (interactive) (find-file (concat org-directory "journal.org"))))
(map! :leader "w" 'kill-buffer-and-window)
(map! :leader "m" '(lambda() (interactive) (org-capture nil "j")))
(map! :leader "a" 'org-agenda)
(map! :map org-mode-map :leader "h" 'export-html-and-open)
(map! :map org-mode-map :leader "t" 'org-to-plaintext-to-clipboard)
