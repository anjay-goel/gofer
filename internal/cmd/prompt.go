package cmd

import (
	"cmp"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	hideCursor   = "\x1b[?25l"
	showCursor   = "\x1b[?25h"
	clearLine    = "\x1b[2K"
	clearBelow   = "\x1b[J"
	spinnerDelay = 150 * time.Millisecond
	spinnerFrame = 80 * time.Millisecond
)

var (
	errCancelled  = exitCode{130, ""}
	spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
)

type choice struct {
	label string
	note  string
}

func interactive() bool {
	return styled && isTTY()
}

func rawTerminal() (func(), error) {
	save := exec.Command("stty", "-g")
	save.Stdin = os.Stdin
	state, err := save.Output()
	if err != nil {
		return nil, err
	}
	set := exec.Command("stty", "-icanon", "-echo", "-isig", "min", "1", "time", "0")
	set.Stdin = os.Stdin
	if err := set.Run(); err != nil {
		return nil, err
	}
	return func() {
		restore := exec.Command("stty", strings.TrimSpace(string(state)))
		restore.Stdin = os.Stdin
		restore.Run()
	}, nil
}

func parseKey(input []byte) string {
	switch string(input) {
	case "\x1b[A", "\x1bOA", "k":
		return "up"
	case "\x1b[B", "\x1bOB", "j":
		return "down"
	case "\r", "\n":
		return "enter"
	case "\x03", "\x1b":
		return "cancel"
	}
	return strings.ToLower(string(input))
}

func readKey() (string, error) {
	buf := make([]byte, 8)
	n, err := os.Stdin.Read(buf)
	if err != nil {
		return "", err
	}
	return parseKey(buf[:n]), nil
}

func question(text string) string {
	return paint(cyan, "?") + " " + paint(bold, text)
}

func answered(text, value string) {
	done("%s %s", text, paint(cyan, value))
}

func step(current, count, delta int) int {
	return (current + delta + count) % count
}

func choose(text string, options []choice) (int, error) {
	if !interactive() {
		return chooseByNumber(text, options)
	}
	restore, err := rawTerminal()
	if err != nil {
		return chooseByNumber(text, options)
	}
	defer restore()
	fmt.Print(hideCursor)
	defer fmt.Print(showCursor)
	fmt.Println(question(text))
	current := 0
	draw := func() {
		for i, option := range options {
			line := "  " + option.label
			if i == current {
				line = paint(cyan, "❯ "+option.label)
			}
			if option.note != "" {
				line += "  " + paint(dim, option.note)
			}
			fmt.Println(clearLine + line)
		}
	}
	draw()
	for {
		key, err := readKey()
		if err != nil {
			return 0, err
		}
		switch key {
		case "up":
			current = step(current, len(options), -1)
		case "down":
			current = step(current, len(options), 1)
		case "enter":
			fmt.Printf("\x1b[%dA%s", len(options)+1, clearBelow)
			answered(text, options[current].label)
			return current, nil
		case "cancel":
			fmt.Printf("\x1b[%dA%s", len(options)+1, clearBelow)
			return 0, errCancelled
		}
		fmt.Printf("\x1b[%dA", len(options))
		draw()
	}
}

func chooseByNumber(text string, options []choice) (int, error) {
	fmt.Println(text)
	for i, option := range options {
		fmt.Printf("  %d. %s", i+1, option.label)
		if option.note != "" {
			fmt.Printf(" (%s)", option.note)
		}
		fmt.Println()
	}
	fmt.Print("Choice: ")
	n, err := strconv.Atoi(readLine())
	if err != nil || n < 1 || n > len(options) {
		return 0, errors.New("that isn't one of the choices")
	}
	return n - 1, nil
}

func askText(text, fallback string) string {
	if !interactive() {
		fmt.Printf("%s [%s]: ", text, fallback)
		return readLine()
	}
	fmt.Printf("%s %s ", question(text), paint(dim, fallback))
	answer := readLine()
	fmt.Print("\x1b[1A" + clearLine + "\r")
	answered(text, cmp.Or(answer, fallback))
	return answer
}

func confirm(text string) (bool, error) {
	if !interactive() {
		fmt.Printf("%s [Y/n] ", text)
		answer := strings.ToLower(readLine())
		return answer == "" || answer == "y" || answer == "yes", nil
	}
	restore, err := rawTerminal()
	if err != nil {
		return false, err
	}
	defer restore()
	fmt.Printf("%s %s ", question(text), paint(dim, "Y/n"))
	for {
		key, err := readKey()
		if err != nil {
			return false, err
		}
		switch key {
		case "enter", "y":
			fmt.Print("\r" + clearLine)
			answered(text, "Yes")
			return true, nil
		case "n":
			fmt.Print("\r" + clearLine)
			answered(text, "No")
			return false, nil
		case "cancel":
			fmt.Print("\r" + clearLine)
			return false, errCancelled
		}
	}
}

func spinning(label string) func() {
	if !styled {
		return func() {}
	}
	finish := make(chan struct{})
	interrupted := make(chan os.Signal, 1)
	signal.Notify(interrupted, os.Interrupt)
	var wait sync.WaitGroup
	wait.Add(1)
	go func() {
		defer wait.Done()
		defer signal.Stop(interrupted)
		select {
		case <-finish:
			return
		case <-interrupted:
			os.Exit(errCancelled.code)
		case <-time.After(spinnerDelay):
		}
		fmt.Print(hideCursor)
		restore := "\r" + clearLine + showCursor
		defer fmt.Print(restore)
		ticker := time.NewTicker(spinnerFrame)
		defer ticker.Stop()
		for frame := 0; ; frame++ {
			fmt.Printf("\r%s %s", paint(cyan, spinnerFrames[frame%len(spinnerFrames)]), paint(dim, label))
			select {
			case <-finish:
				return
			case <-interrupted:
				fmt.Print(restore)
				os.Exit(errCancelled.code)
			case <-ticker.C:
			}
		}
	}()
	var once sync.Once
	return func() {
		once.Do(func() {
			close(finish)
			wait.Wait()
		})
	}
}
