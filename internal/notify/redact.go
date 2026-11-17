// SPDX-FileCopyrightText: 2026 mnnoee
// SPDX-License-Identifier: Apache-2.0

package notify

import (
	"errors"
	"fmt"
	"net/url"
)

// redactEndpoint keeps the scheme and host so an operator can still see where
// an alert was going, but drops the path and query. Webhook URLs and Telegram
// bot URLs carry their secret in the path, so neither may reach a log.
func redactEndpoint(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return "endpoint"
	}
	return u.Scheme + "://" + u.Host + "/***"
}

// redactHTTPError rewrites net/http errors, which embed the full request URL,
// into an error that only contains the redacted endpoint.
func redactHTTPError(err error) error {
	var uerr *url.Error
	if !errors.As(err, &uerr) {
		return err
	}
	cause := uerr.Err
	if cause == nil {
		cause = errors.New("request failed")
	}
	if _, nested := cause.(*url.Error); nested {
		cause = redactHTTPError(cause)
	}
	return fmt.Errorf("%s %s: %w", uerr.Op, redactEndpoint(uerr.URL), cause)
}
