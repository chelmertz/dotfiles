; from http://ergoemacs.org/emacs/emacs_package_system.html
(when (>= emacs-major-version 24)
  (require 'package)
  (add-to-list
   'package-archives
   '("melpa" . "http://melpa.org/packages/")
   t)
  (package-initialize))

;; Added by Package.el.  This must come before configurations of
;; installed packages.  Don't delete this line.  If you don't want it,
;; just comment it out by adding a semicolon to the start of the line.
;; You may delete these explanatory comments.
;;(package-initialize)

(custom-set-variables
 ;; custom-set-variables was added by Custom.
 ;; If you edit it by hand, you could mess it up, so be careful.
 ;; Your init file should contain only one such instance.
 ;; If there is more than one, they won't work right.
 '(calendar-week-start-day 1)
 '(custom-enabled-themes (quote (leuven)))
 '(custom-safe-themes
   (quote
    ("a9c619535d63719a15f22e3c450a03062d3fed1e356ef96d33015849c4c43946" "9d54f3a9cf99c3ffb6ac8e84a89e8ed9b8008286a81ef1dbd48d24ec84efb2f1" "0c9f63c9d90d0d135935392873cd016cc1767638de92841a5b277481f1ec1f4a" default)))
 '(erc-modules
   (quote
    (autojoin button completion fill irccontrols keep-place list match menu move-to-prompt netsplit networks noncommands notifications readonly ring stamp track)))
 '(global-flycheck-mode t)
 '(markdown-command "pandoc --from markdown --to html5")
 '(org-adapt-indentation nil)
 '(org-descriptive-links nil)
 '(org-export-with-properties t)
 '(org-export-with-sub-superscripts nil)
 '(package-selected-packages
   (quote
    (projectile flycheck-color-mode-line flycheck solidity-flycheck htmlize company tide go-mode lsp-ui lsp-mode doom-themes zenburn-theme darktooth-theme dune evil-collection elfeed-org elfeed evil-org tuareg atom-dark-theme haskell-mode org feature-mode evil-vimish-fold yaml-mode gruvbox-theme php-mode markdown-mode lua-mode evil-magit magit evil-leader evil-nerd-commenter evil-matchit cider evil ##))))

(add-to-list 'default-frame-alist '(fullscreen . maximized))

(custom-set-faces
 ;; custom-set-faces was added by Custom.
 ;; If you edit it by hand, you could mess it up, so be careful.
 ;; Your init file should contain only one such instance.
 ;; If there is more than one, they won't work right.
 )

;; to edit the rules above, see M-x customize-mode

;; do not make bell sound or flash
(setq ring-bell-function 'ignore)
;; do not show startup screen
(setq inhibit-startup-screen t)
;; stop asking if I want to follow symlinks, I do
(setq vc-follow-symlinks nil)
;; show line numbers
(global-linum-mode t)

(if (display-graphic-p)
    (load-theme 'doom-nord-light t))

(setq visual-line-mode t)

;; how to handle emacs backup files (like vim's swap files)
;; from https://stackoverflow.com/questions/151945/how-do-i-control-how-emacs-makes-backup-files
(defvar --backup-directory (concat user-emacs-directory "backups"))
(if (not (file-exists-p --backup-directory))
        (make-directory --backup-directory t))
(setq backup-directory-alist `(("." . ,--backup-directory)))
(setq make-backup-files t      ; backup of a file the first time it is saved.
      backup-by-copying t      ; don't clobber symlinks
      version-control t        ; version numbers for backup files
      delete-old-versions t    ; delete excess backup files silently
      kept-old-versions 6      ; oldest versions to keep when a new numbered backup is made (default: 2)
      kept-new-versions 9      ; newest versions to keep when a new numbered backup is made (default: 2)
      auto-save-default t      ; auto-save every buffer that visits a file
      auto-save-timeout 60     ; number of seconds idle time before auto-save (default: 30)
      auto-save-interval 200   ; number of keystrokes between auto-saves (default: 300)
      )

(setq erc-hide-list '("JOIN" "PART" "QUIT" "NICK" "KICK" "MODE"))

; org-tempo is used for e.g. expanding < + s + TAB to a code block
(require 'org-tempo)
(setq org-directory "~/Dropbox/orgzly")
(setq org-agenda-files (list org-directory))
(setq org-default-notes-file (concat org-directory "/inbox.org"))
(setq org-capture-templates
      '(
	("t" "TODO" entry (file "inbox.org") "* TODO %?\n:PROPERTIES:\n:CREATED: %T\n:END:\n\n  %i")
	("j" "Journal" entry (file+datetree "journal.org") "* %?\n:PROPERTIES:\n:CREATED: %T\n:END:\n" :jump-to-captured t)
	("e" "Elvaco TODO" entry (file+headline "elvaco.org" "TODOs")
	 "* TODO %?\n:PROPERTIES:\n:CREATED: %T\n:END:\n\n  %i")
	("r" "Elvaco retrospective" entry (file+headline "elvaco.org" "Retrospective")
	 "* TODO %?\n:PROPERTIES:\n:CREATED: %T\n:END:\n\n  %i")
	)
      )
(setq org-log-done 'time)
(setq org-tag-alist '(
		      (:startgrouptag)
		      ("family")
		      (:grouptags)
		      ("Alfred")
		      ("Linnéa")
		      (:endgrouptag)

		      (:startgrouptag)
		      ("emacs")
		      (:grouptags)
		      ("org")
		      (:endgrouptag)

		      (:startgrouptag)
		      ("reviews")
		      (:grouptags)
		      ("games")
		      ("books")
		      (:endgrouptag)

		      (:startgrouptag)
		      ("games")
		      (:grouptags)
		      ("ps4")
		      ("pc")
		      (:endgrouptag)
		      ))

(require 'org)
(require 'ob-clojure)
(setq org-babel-clojure-backend 'cider)
(require 'cider)
(org-babel-do-load-languages 'org-babel-load-languages
    '(
        (shell . t)
        (clojure . t)
        (haskell . t)
        (ocaml . t)
        (sql . t)
    )
)

; see https://orgmode.org/manual/Code-evaluation-security.html
(setq org-confirm-babel-evaluate nil)

; see https://emacs.stackexchange.com/questions/14788/org-mode-refile-to-other-files-does-not-work
(setq org-refile-targets '(
			   (nil :maxlevel . 2)
			   (org-agenda-files :maxlevel . 2)
			   ))

; see https://emacs.stackexchange.com/questions/13353/how-to-use-org-refile-to-move-a-headline-to-a-file-as-a-toplevel-headline
(setq org-refile-use-outline-path 'file)

(setq org-return-follows-link t)

(setq org-html-doctype "html5")

; show link under pointer when hovering with mouse
(setq help-at-pt-display-when-idle t)

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

(defun org-export-to-html-and-open ()
  (interactive)
  (org-open-file (org-html-export-to-html)))

(require 'elfeed-org)
(elfeed-org)
(setq rmh-elfeed-org-files (list (concat org-directory "/feeds.org")))
; see https://kitchingroup.cheme.cmu.edu/blog/2017/01/05/Scoring-elfeed-articles/
(defface personal-elfeed-entry
  `((t :background ,(color-lighten-name "orange1" 40)))
  "Marks a personal Elfeed entry.")
(push '(personal personal-elfeed-entry)
      elfeed-search-face-alist)


(setq org-todo-keywords
      '((sequence "TODO" "|" "DONE" "WONT")))

;; evil-collection needs evil to load with these settings
(setq evil-want-integration t)
(setq evil-want-keybinding nil)
;; "You should enable global-evil-leader-mode before you enable
;; evil-mode, otherwise evil-leader won’t be enabled in initial
;; buffers (*scratch*, *Messages*, …).", from
;; https://github.com/cofi/evil-leader
(require 'evil-leader)
(global-evil-leader-mode)
(evil-leader/set-leader ",")

; https://emacs.stackexchange.com/questions/46371/how-can-i-get-ret-to-follow-org-mode-links-when-using-evil-mode
(with-eval-after-load 'evil-maps
  (define-key evil-motion-state-map (kbd "RET") nil))

;; see https://github.com/Somelauw/evil-org-mode for key bindings etc
(require 'evil-org)
(add-hook 'org-mode-hook 'evil-org-mode)
(add-hook 'org-mode-hook '(lambda ()
			    (setq visual-line-mode t)
			    (setq truncate-lines nil) ; soft wrap
			    ))
(add-hook 'org-mode-hook
          (lambda ()
        (define-key evil-normal-state-map (kbd "TAB") 'org-cycle)))
(evil-org-set-key-theme '(navigation insert textobjects additional calendar))
(require 'evil-org-agenda)
(evil-org-agenda-set-keys)

(require 'lsp-mode)
(add-hook 'go-mode-hook #'lsp-deferred)
; auto completion
(require 'lsp-ui)

; tide, for typescript - https://github.com/ananthakumaran/tide
(defun setup-tide-mode ()
  (interactive)
  (tide-setup)
  (flycheck-mode +1)
  (setq flycheck-check-syntax-automatically '(save mode-enabled))
  (eldoc-mode +1)
  (tide-hl-identifier-mode +1)
  ;; company is an optional dependency. You have to
  ;; install it separately via package-install
  ;; `M-x package-install [ret] company`
  (company-mode +1))

;; formats the buffer before saving
(add-hook 'before-save-hook 'tide-format-before-save)

(add-hook 'typescript-mode-hook #'setup-tide-mode)

; company, auto completion
(setq company-tooltip-align-annotations t)
(add-hook 'after-init-hook 'global-company-mode)

(add-hook 'php-mode-hook '(lambda ()
			    (setq indent-tabs-mode t
				  tab-width 4
				  c-basic-offset 4)))
(add-hook 'js-mode-hook '(lambda ()
			    (setq indent-tabs-mode t
				  tab-width 4
				  c-basic-offset 4)))
(add-hook 'ruby-mode-hook '(lambda ()
			    (setq tab-width 2
				  c-basic-offset 2)))
(add-hook 'feature-mode-hook '(lambda()
			    (setq indent-tabs-mode t
				  tab-with 4
				  c-basic-offset 4)))
(add-hook 'haskell-mode-hook #'hindent-mode)

(add-hook 'text-mode-hook 'flyspell-mode)
(add-hook 'text-mode-hook 'turn-on-auto-fill)
(add-hook 'prog-mode-hook 'flyspell-prog-mode)

(add-hook 'before-save-hook 'delete-trailing-whitespace)

;; ocaml
(let ((opam-share (ignore-errors (car (process-lines "opam" "config" "var" "share")))))
  (when (and opam-share (file-directory-p opam-share))
    (add-to-list 'load-path (expand-file-name "emacs/site-lisp" opam-share))
    (autoload 'merlin-mode "merlin" nil t nil)
    (add-hook 'tuareg-mode-hook 'merlin-mode t)
    (add-hook 'caml-mode-hook 'merlin-mode t)))

;; helm, an autocompleter (maybe like vim's ctrlp?)
;; while in the 'helm-find-files view, C-s will enter "ack mode"
(require 'helm)
(require 'helm-config)
(helm-mode 1)
(helm-autoresize-mode t)
(setq helm-find-noerrors t)

;; use tab to auto complete (C-n from evil-mode also works)
(setq tab-always-indent 'complete)

;; override the builtin file finder with helm's variant
;; separate search terms with space
(global-set-key (kbd "C-x C-f") 'helm-find)
(global-set-key (kbd "<f2>") 'merlin-error-next)
(global-set-key (kbd "<f6>") 'projectile-replace)

;; (setq helm-ff-skip-boring-files t)
;; (setq helm-boring-file-regexp-list '("^tags$"))

(defun work-journal ()
  (interactive)
  (find-file (expand-file-name "~/Dropbox/orgzly/elvaco.org")))

(defun edit-ansible ()
  (interactive)
  (find-file (expand-file-name "~/code/github/chelmertz/dotfiles/ansible-laptop.yml")))

(defun capture-into-journal ()
  (interactive)
  (org-capture nil "j"))

(defun view-journal ()
  (interactive)
  (find-file (expand-file-name "~/Dropbox/orgzly/journal.org")))

(evil-leader/set-key
 "c" 'org-capture
 "j" 'view-journal
 "." 'capture-into-journal
 "a" 'org-agenda
 "e" 'work-journal
 "," 'helm-buffers-list
 "r" 'org-refile
 "w" 'kill-buffer-and-window
 "m" 'edit-ansible
 "t" 'org-to-plaintext-to-clipboard
 "h" 'org-export-to-html-and-open
)

(define-key evil-normal-state-map (kbd "gx") 'browse-url-at-point)

;; C-u is already taken by emacs for *something* (no idea yet) but I
;; use it a lot in evil mode, so, let's hijack it within the evil
;; domain
(define-key evil-normal-state-map (kbd "C-u") 'evil-scroll-up)

;; make sure that search & replace in evil is global by default
(setq evil-ex-substitute-global t)

;; make * search for whole a_variable, instead of either "a" or
;; "variable"
(setq-default evil-symbol-word-search t)

(require 'evil)
(when (require 'evil-collection nil t)
  (evil-collection-init))
(evil-mode 1)

;; enable folds (which were disabled in at least Ruby)
(require 'evil-vimish-fold)
(evil-vimish-fold-mode 1)

;; enable vim keys while in magit, to avoid learning movement commands
(require 'evil-magit)

;; in vim this means "vnoremap > >gv", to re-enter visual mode after
;; shifting indent: http://superuser.com/a/489121/9539
(define-key evil-visual-state-map ">" (lambda ()
    (interactive)
    ; ensure mark is less than point
    (when (> (mark) (point))
        (exchange-point-and-mark)
    )
    (evil-normal-state)
    (evil-shift-right (mark) (point))
    (evil-visual-restore) ; re-select last visual-mode selection
))

(define-key evil-visual-state-map "<" (lambda ()
    (interactive)
    ; ensure mark is less than point
    (when (> (mark) (point))
        (exchange-point-and-mark)
    )
    (evil-normal-state)
    (evil-shift-left (mark) (point))
    (evil-visual-restore) ; re-select last visual-mode selection
))


(require 'evil-surround)
(global-evil-surround-mode 1)

(require 'evil-matchit)
(global-evil-matchit-mode 1)

(evilnc-default-hotkeys)

;; small guide for an emacs noob:
;;
;; M-x ;; (alt+x) is akin close to vim's :e (command mode), I
;; think.. but M-: is also some sort of command mode
;; C-g ;; cancel current command
;; C-x C-s ;; save current file
;; C-x C-e ;; execute expression under cursor ("apply" new config in .emacs for example)
;; C-x Cf ;; find file, (helm adds: put spaces in between search terms)
;; M-x package-install RET package-name RET ;; install package-name
;; M-x package-refresh-contents RET ;; if the package-install fails
;; M-x eval-buffer ;; reload .emacs without restarting emacs (if
;; .emacs is the file currently being edited
;;
;;
;; help
;;
;; C-h f ;; emacs functions
;; C-h v ;; emacs variables
;; C-h m ;; help scoped to currently loaded major/minor modes
;;
;;
;; buffers
;;
;; ,b ;; show all buffers in a helm, auto complete, MRU
;;
;;
;; markdown
;;
;; C-c C-c l ;; live preview in buffer
;; C-c C-c p ;; live preview in browser
;; C-c C-c e ;; export to html file
;;
;;
;; packages
;;
;; M-x package-install ;; install a package
;; M-x package-list-packages ;; installing packages from here sometimes "works better" :(
;;
;; if I start emacs from a directory, I don't want 'helm-find to
;; change that directory for me, so don't do C-x C-f from the current
;; file, but change to the scratch buffer in between (with C-x b), and
;; then use C-x C-f. This will reuse the original current working
;; directory. From: http://stackoverflow.com/a/2627086/49879
;;
;;
;; org mode
;;
;; <s TAB ;; new code block
;; C-c C-c ;; execute code in babel
;; C-c C-t ;; toggle TODO state
;; C-c C-s ;; schedule entry
;; C-c C-d ;; deadline entry
;; ,a ;; agenda
;; C-x n s ;; narrow on entry (zoom in)
;; C-x n w ;; widen (zoom out again)
;; C-c C-o ;; follow link under cursor
;; C-c C-e ;; export dialog (h o will export to html and open in browser)
;;
;;
;; elfeed search
;;
;; s ;; search
;; gr ;; remove entries not matching filter
;; gR ;; trigger remote update
;; u ;; mark read
;; U ;; mark unread
;; + ;; add tag
;; - ;; remove tag
;;
;;
;; elfeed entry
;;
;; S-RETURN ;; open url in browser
;; + ;; add tag
;; - ;; remove tag
