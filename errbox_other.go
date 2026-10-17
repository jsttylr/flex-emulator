//go:build !windows

package main

// showError does nothing here: fatal already logs the message to the terminal.
func showError(msg string) {}
