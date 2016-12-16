;; from http://ergoemacs.org/emacs/emacs_package_system.html
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
 '(markdown-command "pandoc --from markdown --to html5")
 '(package-selected-packages
   (quote
    (feature-mode evil-vimish-fold yaml-mode gruvbox-theme php-mode markdown-mode lua-mode evil-magit helm magit evil-leader evil-nerd-commenter evil-matchit cider evil ##))))
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

(load-theme 'gruvbox t)

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
      auto-save-timeout 20     ; number of seconds idle time before auto-save (default: 30)
      auto-save-interval 200   ; number of keystrokes between auto-saves (default: 300)
      )

;; use tabs in favor of spaces in some file types
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

;; helm, an autocompleter (maybe like vim's ctrlp?)
;; while in the 'helm-find-files view, C-s will enter "ack mode"
(require 'helm)
(require 'helm-config)
(helm-mode 1)
(helm-autoresize-mode t)

;; override the builtin file finder with helm's variant
;; separate search terms with space
(global-set-key (kbd "C-x C-f") 'helm-find)

;; (setq helm-ff-skip-boring-files t)
;; (setq helm-boring-file-regexp-list '("^tags$"))

;; "You should enable global-evil-leader-mode before you enable
;; evil-mode, otherwise evil-leader won’t be enabled in initial
;; buffers (*scratch*, *Messages*, …).", from
;; https://github.com/cofi/evil-leader
(require 'evil-leader)
(global-evil-leader-mode)
(evil-leader/set-leader ",")

;; evil-nerd-commenter, toggles comments from visual selections
;; leader + c (,+c) will toggle the current row
(evil-leader/set-key
 "c" 'evilnc-comment-or-uncomment-lines
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
;; M-x ;; (alt+x) is akin close to vim's :e (command mode), I
;; think.. but M-: is also some sort of command mode
;; C-g ;; cancel current command
;; C-x C-s ;; save current file
;; C-x Cf ;; find file, (helm adds: put spaces in between search terms)
;; M-x package-install RET package-name RET ;; install package-name
;; M-x eval-buffer ;; reload .emacs without restarting emacs (if
;; .emacs is the file currently being edited
;;
;; buffers, from
;; https://www.gnu.org/software/emacs/manual/html_node/emacs/Select-Buffer.html
;; C-x b RET ;; switch to previous buffer, "cd -" style
;; C-x b buffername RET ;; select buffer or create new. tab completion
;; works
;; C-x LEFT ;; prev buffer, C-x RIGHT works too
;; C-x k ENT ;; kill current buffer
;; M-: buffer-file-name ;; display the filename of the current buffer
;;
;; markdown
;; C-c C-c l ;; live preview in buffer
;; C-c C-c p ;; live preview in browser
;; C-c C-c e ;; export to html file
;;
;; packages
;; M-x package-install ;; install a package
;; M-x package-list-packages ;; installing packages from here sometimes "works better" :(
;;
;; if I start emacs from a directory, I don't want 'helm-find to
;; change that directory for me, so don't do C-x C-f from the current
;; file, but change to the scratch buffer in between (with C-x b), and
;; then use C-x C-f. This will reuse the original current working
;; directory. From: http://stackoverflow.com/a/2627086/49879
