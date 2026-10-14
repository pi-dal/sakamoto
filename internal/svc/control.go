package svc

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"syscall"

	"github.com/pi-dal/sakamoto/internal/sbclient"
)

type controlRequest struct {
	Tag      string `json:"tag,omitempty"`
	Group    string `json:"group,omitempty"`
	Outbound string `json:"outbound,omitempty"`
}

func (s *Server) apiClient(ctx context.Context) (*sbclient.Client, error) {
	if err := validateAPIService(s.cfgPath); err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(s.cfgPath)
	if err != nil {
		return nil, err
	}
	var cfg struct {
		Services []struct {
			Type, Secret string
			ListenPort   uint16 `json:"listen_port"`
		} `json:"services"`
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return nil, err
	}
	for _, service := range cfg.Services {
		if service.Type == "api" {
			return sbclient.Dial(ctx, fmt.Sprintf("http://127.0.0.1:%d", service.ListenPort), service.Secret)
		}
	}
	return nil, fmt.Errorf("authenticated core API is unavailable")
}

func (s *Server) control(verb, arguments string) string {
	ctx, cancel := context.WithTimeout(context.Background(), sbclient.URLTestRequestTimeout)
	defer cancel()
	if verb == "reload" {
		return s.reload(ctx)
	}
	var request controlRequest
	if verb != "state" {
		decoder := json.NewDecoder(strings.NewReader(arguments))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&request); err != nil {
			return "error: invalid control arguments"
		}
		if err := decoder.Decode(&struct{}{}); err != io.EOF {
			return "error: invalid control arguments"
		}
	} else if arguments != "" {
		return "error: state takes no arguments"
	}
	client, err := s.apiClient(ctx)
	if err != nil {
		return "error: " + err.Error()
	}
	defer client.Close()
	switch verb {
	case "urltest":
		if strings.TrimSpace(request.Tag) == "" {
			return "error: tag is required"
		}
		if err := client.URLTest(ctx, request.Tag); err != nil {
			return "error: " + err.Error()
		}
		return "URL test queued: " + request.Tag
	case "select":
		if request.Group == "" || request.Outbound == "" {
			return "error: group and outbound are required"
		}
		if err := client.SelectOutbound(ctx, request.Group, request.Outbound); err != nil {
			return "error: " + err.Error()
		}
		return "Selected " + request.Outbound + " in " + request.Group
	case "state":
		groups, errors := client.SubscribeGroups(ctx)
		select {
		case snapshot := <-groups:
			if snapshot == nil {
				return "error: core group stream ended"
			}
			raw, err := json.Marshal(struct {
				Status string `json:"status"`
				Groups any    `json:"groups"`
			}{s.status(), snapshot})
			if err != nil {
				return "error: " + err.Error()
			}
			return string(raw)
		case err := <-errors:
			return "error: " + err.Error()
		case <-ctx.Done():
			return "error: " + ctx.Err().Error()
		}
	}
	return "error: unsupported control command"
}

// Native run handles SIGHUP. Validate before requesting a reload and retain the
// supervisor's DNS lifecycle; changing DNS protection requires reconnecting.
func (s *Server) reload(ctx context.Context) string {
	s.lifecycle.Lock()
	defer s.lifecycle.Unlock()
	s.mu.Lock()
	running, child, dnsStatus := s.runningLocked(), s.child, s.dnsStatus
	s.mu.Unlock()
	if !running {
		return "error: core is not running"
	}
	if err := validateAPIService(s.cfgPath); err != nil {
		return "error: " + err.Error()
	}
	_, guarded, err := s.dnsPlan()
	if err != nil {
		return "error: " + err.Error()
	}
	if guarded != (dnsStatus != "off" && dnsStatus != "failed") {
		return "error: DNS protection changed; disconnect and reconnect to apply"
	}
	command := exec.CommandContext(ctx, singBoxExecutable(), "check", "-D", s.workDir, "-c", s.cfgPath)
	if err := command.Run(); err != nil {
		return "error: config validation failed; core was not reloaded"
	}
	if err := child.Process.Signal(syscall.SIGHUP); err != nil {
		return "error: " + err.Error()
	}
	return "reload requested; verify state and logs"
}
