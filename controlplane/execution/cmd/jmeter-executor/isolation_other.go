//go:build !linux

package main

import (
	"errors"
	"syscall"
)

func protectParent() error                 { return errors.New("execution isolation requires Linux") }
func childIdentity(a *syscall.SysProcAttr) {}
func reapEngine() error                    { return errors.New("process isolation requires Linux") }
