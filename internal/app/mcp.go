package app

import (
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

var mcpEnvName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func validateMCPServerSource(server mcpServer) error {
	command, address := strings.TrimSpace(server.Command), strings.TrimSpace(server.URL)
	if command == "" && address == "" {
		return errors.New("server command or url must not be empty")
	}
	if command != "" && address != "" {
		return errors.New("server must specify either command or url")
	}
	if command != "" {
		if len(server.Headers) > 0 || server.BearerTokenEnvVar != "" {
			return errors.New("headers and bearer_token_env_var require url")
		}
		return nil
	}
	if len(server.Args) > 0 || len(server.Env) > 0 {
		return errors.New("args and env require command")
	}
	parsed, err := url.Parse(address)
	if err != nil || parsed.Host == "" || parsed.User != nil || parsed.Fragment != "" || (parsed.Scheme != "https" && !(parsed.Scheme == "http" && isLocalMCPHost(parsed.Hostname()))) {
		return errors.New("url must be HTTPS (or HTTP on localhost) without credentials or fragment")
	}
	if server.BearerTokenEnvVar != "" && !mcpEnvName.MatchString(server.BearerTokenEnvVar) {
		return errors.New("bearer_token_env_var must be an environment variable name")
	}
	for key, value := range server.Headers {
		if strings.EqualFold(key, "Authorization") {
			return errors.New("use bearer_token_env_var for Authorization")
		}
		if strings.TrimSpace(key) == "" || strings.ContainsAny(key, " \t\r\n") {
			return fmt.Errorf("invalid header name %q", key)
		}
		if strings.Contains(value, "${") {
			return fmt.Errorf("header %q cannot contain an environment reference; use bearer_token_env_var for bearer auth", key)
		}
	}
	return nil
}

func isLocalMCPHost(host string) bool {
	return strings.EqualFold(host, "localhost") || host == "127.0.0.1" || host == "::1"
}
