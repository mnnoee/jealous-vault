// SPDX-FileCopyrightText: 2026 mnnoee
// SPDX-License-Identifier: Apache-2.0

package config

import (
	"errors"
	"fmt"
	"net/mail"
	"strings"
)

func validateEmail(email Email, password string) error {
	switch email.Mode {
	case "local", "smtp":
	default:
		return fmt.Errorf("notify.email.mode %q must be local or smtp", email.Mode)
	}
	if err := validateAddress("notify.email.from", email.From); err != nil {
		return err
	}
	if len(email.To) == 0 {
		return errors.New("notify.email.to is empty")
	}
	for i, address := range email.To {
		if err := validateAddress(fmt.Sprintf("notify.email.to[%d]", i), address); err != nil {
			return err
		}
	}
	switch email.Security {
	case "tls", "starttls", "none":
	default:
		return fmt.Errorf("notify.email.security %q must be tls, starttls or none", email.Security)
	}
	if email.Mode == "smtp" && email.Host == "" {
		return errors.New("notify.email.host is required for smtp mode")
	}
	if email.Mode == "local" && email.Username != "" {
		return errors.New("notify.email.username is not supported with mode local")
	}
	if email.Port < 1 || email.Port > 65535 {
		return fmt.Errorf("notify.email.port %d is out of range", email.Port)
	}
	if email.Username != "" && password == "" {
		return errors.New("notify.email.password_file or password_env is required with username")
	}
	if email.Security == "none" && email.Username != "" {
		return errors.New("notify.email.security none cannot be used with authentication")
	}
	return nil
}

func validateAddress(key, address string) error {
	address = strings.TrimSpace(address)
	if address == "" {
		return fmt.Errorf("%s is empty", key)
	}
	if _, err := mail.ParseAddress(address); err != nil {
		return fmt.Errorf("%s %q is not a valid address", key, address)
	}
	return nil
}
