package ui

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/term"
)

func Prompt(r *bufio.Reader, msg string) string {
	fmt.Print(msg)
	line, _ := r.ReadString('\n')
	return strings.TrimSpace(line)
}

func MustPrompt(r *bufio.Reader, msg string) string {
	for {
		if v := Prompt(r, msg); v != "" {
			return v
		}
		fmt.Fprintln(os.Stderr, "  (value required, please enter a value)")
	}
}

func PromptWithDefault(r *bufio.Reader, label, dflt string) string {
	v := Prompt(r, fmt.Sprintf("%s [%s]: ", label, dflt))
	if v == "" {
		return dflt
	}
	return v
}

func OptionalPrompt(r *bufio.Reader, msg string) string {
	return Prompt(r, msg)
}

func PromptPassword(r *bufio.Reader, msg string) string {
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		return MustPrompt(r, msg)
	}
	fmt.Print(msg)
	b, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Println()
	if err != nil {
		Fatalf("reading password: %v\n", err)
	}
	if len(b) == 0 {
		Fatalf("password is required\n")
	}
	return string(b)
}

func ConfirmOverwrite(r *bufio.Reader, serverPath string) bool {
	v := Prompt(r, fmt.Sprintf("  %s already exists. Overwrite? [y/N]: ", serverPath))
	return strings.EqualFold(v, "y") || strings.EqualFold(v, "yes")
}

func BaseName(path string) string {
	base := filepath.Base(path)
	return strings.TrimSuffix(base, filepath.Ext(base))
}

func Fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "ERROR: "+format, args...)
	os.Exit(1)
}
