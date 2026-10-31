package cmd

import (
	"errors"
	"fmt"
	"strings"

	"github.com/anjay-goel/gofer/internal/core"
	"github.com/anjay-goel/gofer/internal/transport"
)

func messageWords(args []string) (string, error) {
	if len(args) > 0 && args[0] == "--" {
		args = args[1:]
	}
	message := strings.Join(args, " ")
	if strings.TrimSpace(message) == "" {
		return "", errors.New("the message is empty")
	}
	return message, nil
}

func (a *app) send(args []string) error {
	if len(args) < 2 {
		return errors.New("usage: gofer send <name> \"<message>\"")
	}
	name := args[0]
	message, err := messageWords(args[1:])
	if err != nil {
		return err
	}
	r, err := spinCall(a.client, "Sending to "+name, core.Send, core.SendParams{Name: name, Message: message})
	if err != nil {
		var network transport.NetworkError
		switch {
		case !errors.As(err, &network):
			return err
		case !network.Retryable():
			return fmt.Errorf("%w. Fix SSH login to %s, then try again", err, a.host)
		}
		return exitCode{exitUnconfirmed, fmt.Sprintf("%s. The message may have landed anyway, so check gofer logs %s before resending", err, name)}
	}
	if r.Delivered {
		if r.Action == "resumed" {
			done("Woke %s and delivered your message", name)
		} else {
			done("Delivered to %s", name)
		}
		return nil
	}
	return exitCode{exitUnconfirmed, fmt.Sprintf("Sent to %s (%s), delivery not confirmed: %s", name, r.Action, r.Note)}
}
