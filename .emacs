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
 '(package-selected-packages
   (quote
    (helm magit evil-leader evil-nerd-commenter evil-matchit cider evil ##))))
(custom-set-faces
 ;; custom-set-faces was added by Custom.
 ;; If you edit it by hand, you could mess it up, so be careful.
 ;; Your init file should contain only one such instance.
 ;; If there is more than one, they won't work right.
 )

;; do not show startup screen
(setq inhibit-startup-screen t)
;; stop asking if I want to follow symlinks, I do
(setq vc-follow-symlinks nil)

;; helm, an autocompleter (maybe like vim's ctrlp?)
(require 'helm)
(require 'helm-config)
(helm-mode 1)
(helm-autoresize-mode t)
(global-set-key (kbd "C-SPC") 'helm-find-files) ;; this overrides
;; Emacs' marks, but I can still use evil's marks

;; "You should enable global-evil-leader-mode before you enable
;; evil-mode, otherwise evil-leader won’t be enabled in initial
;; buffers (*scratch*, *Messages*, …).", from
;; https://github.com/cofi/evil-leader
(require 'evil-leader)
(global-evil-leader-mode)
(evil-leader/set-leader ",")
(evil-leader/set-key
 "c" 'evilnc-comment-or-uncomment-lines
 )

(require 'evil)
(evil-mode 1)

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

;; evil-nerd-commenter, toggles comments from visual selections
(evilnc-default-hotkeys)


;; small guide for an emacs noob:
;; M-x ;; (alt+x) is akin close to vim's :e (command mode), I
;; think.. but M-: is also some sort of command mode
;; C-g ;; cancel current command
;; C-x C-s ;; save current file
;; C-x Cf ;; find file
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
;; C-x k ENT ;; current buffer
;; M-: buffer-file-name ;; display the filename of the current buffer
