#!/usr/bin/env python

from ctypes import *

class Display(Structure):
    """ opaque struct """

X11 = cdll.LoadLibrary("libX11.so.6")
X11.XOpenDisplay.restype = POINTER(Display)

display = X11.XOpenDisplay(c_int(0))
X11.XkbLockModifiers(display, c_uint(0x0100), c_uint(2), c_uint(0))
X11.XCloseDisplay(display)
