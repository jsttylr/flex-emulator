//go:build !windows

package main

import "fmt"
import "os"

func showError(msg string) { fmt.Fprintln(os.Stderr, msg) }
