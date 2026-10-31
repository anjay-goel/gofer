package cmd

import (
	"bufio"
	"cmp"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

const (
	hideCursor    = "\x1b[?25l"
	showCursor    = "\x1b[?25h"
	clearLine     = "\x1b[2K"
	clearBelow    = "\x1b[J"
	spinnerDelay  = 150 * time.Millisecond
	spinnerFrame  = 80 * time.Millisecond
	spinnerGutter = 2
)

var (
	errCancelled  = exitCode{130, ""}
	spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
	stdin         = bufio.NewReader(os.Stdin)
)

type choice struct {
	label string
	note  string
}

func stdinIsTerminal() bool {
	return isTerminal(os.Stdin)
}

func interactive() bool {
	return styled && stdinIsTerminal()
}

func readLine() string {
	line, _ := stdin.ReadString('\n')
	return strings.TrimSpace(line)
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
	restore := func() {
		reset := exec.Command("stty", strings.TrimSpace(string(state)))
		reset.Stdin = os.Stdin
		reset.Run()
	}
	caught := make(chan os.Signal, 1)
	signal.Notify(caught, syscall.SIGTERM)
	leaving := make(chan struct{})
	go func() {
		select {
		case <-caught:
			restore()
			fmt.Print(showCursor)
			os.Exit(errCancelled.code)
		case <-leaving:
		}
	}()
	return func() {
		signal.Stop(caught)
		close(leaving)
		restore()
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
	if width := terminalWidth(); width > spinnerGutter {
		label = clipWidth(label, width-spinnerGutter)
	}
	finish := make(chan struct{})
	caught := make(chan os.Signal, 1)
	signal.Notify(caught, os.Interrupt, syscall.SIGTERM)
	var wait sync.WaitGroup
	wait.Add(1)
	go func() {
		defer wait.Done()
		defer signal.Stop(caught)
		shown := false
		erase := func() {
			if shown {
				fmt.Print("\r" + clearLine + showCursor)
			}
		}
		defer erase()
		next := time.After(spinnerDelay)
		for frame := 0; ; frame++ {
			select {
			case <-finish:
				return
			case <-caught:
				erase()
				os.Exit(errCancelled.code)
			case <-next:
			}
			if !shown {
				fmt.Print(hideCursor)
				shown = true
			}
			fmt.Printf("\r%s %s", paint(cyan, spinnerFrames[frame%len(spinnerFrames)]), paint(dim, label))
			next = time.After(spinnerFrame)
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
