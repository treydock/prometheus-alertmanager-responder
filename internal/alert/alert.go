// Copyright 2022 Trey Dockendorf
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package alert

import (
	"time"

	"github.com/go-kit/kit/log"
	"github.com/go-kit/kit/log/level"
	"github.com/prometheus/alertmanager/template"
	"github.com/treydock/alertmanager-command-responder/internal/config"
)

const (
	userAnnotation         = "command_responder_user"
	sshKeyAnnotation       = "command_responder_ssh_key"
	sshHostAnnotation      = "command_responder_ssh_host"
	sshCommandAnnotation   = "command_responder_ssh_command"
	sshCommandTimeout      = "command_responder_ssh_command_timeout"
	localCommandAnnotation = "command_responder_local_command"
	localCommandTimeout    = "command_responder_local_command_timeout"
)

type Alert struct {
	template.Alert
	logger   log.Logger
	Response AlertResponse `json:"response"`
}

type AlertResponse struct {
	User                 string        `json:"user"`
	SSHKey               string        `json:"ssh_key"`
	SSHPassword          string        `json:"ssh_password"`
	SSHKnownHosts        string        `json:"ssh_known_hosts"`
	SSHHostKeyAlgorithms []string      `json:"ssh_host_key_algorithms"`
	SSHConnectionTimeout time.Duration `json:"ssh_connection_timeout"`
	SSHCommandTimeout    time.Duration `json:"ssh_command_timeout"`
	SSHHost              string        `json:"ssh_host"`
	SSHCommand           string        `json:"ssh_command"`
	LocalCommand         string        `json:"local_command"`
	LocalCommandTimeout  time.Duration `json:"local_command_timeout"`
}

func (a *Alert) Name() string {
	if val, ok := a.Alert.Labels["alertname"]; ok {
		return val
	}
	return a.Alert.Fingerprint
}

func (a *Alert) HandleAlert(c *config.Config, logger log.Logger) error {
	a.logger = log.With(logger, "alert", a.Alert.Fingerprint, "alertname", a.Name())
	level.Debug(a.logger).Log("msg", "Handling alert")
	r := AlertResponse{
		User:                 c.User,
		SSHKey:               c.SSHKey,
		SSHPassword:          c.SSHPassword,
		SSHKnownHosts:        c.SSHKnownHosts,
		SSHHostKeyAlgorithms: c.SSHHostKeyAlgorithms,
		SSHCommandTimeout:    c.SSHCommandTimeout,
		LocalCommandTimeout:  c.LocalCommandTimeout,
	}
	if val, ok := a.Alert.Annotations[userAnnotation]; ok {
		r.User = val
	}
	if val, ok := a.Alert.Annotations[sshKeyAnnotation]; ok {
		r.SSHKey = val
	}
	if val, ok := a.Alert.Annotations[sshHostAnnotation]; ok {
		r.SSHHost = val
	}
	if val, ok := a.Alert.Annotations[sshCommandAnnotation]; ok {
		r.SSHCommand = val
	}
	if val, ok := a.Alert.Annotations[sshCommandTimeout]; ok {
		timeout, err := time.ParseDuration(val)
		if err == nil {
			r.SSHCommandTimeout = timeout
		} else {
			level.Error(a.logger).Log("msg", "Unable to parse SSH command timeout", "err", err, "timeout", val)
		}
	}
	if val, ok := a.Alert.Annotations[localCommandAnnotation]; ok {
		r.LocalCommand = val
	}
	if val, ok := a.Alert.Annotations[localCommandTimeout]; ok {
		timeout, err := time.ParseDuration(val)
		if err == nil {
			r.LocalCommandTimeout = timeout
		} else {
			level.Error(a.logger).Log("msg", "Unable to parse local command timeout", "err", err, "timeout", val)
		}
	}
	a.Response = r

	var err error
	start := time.Now()
	if a.Response.LocalCommand != "" {
		localLogger := log.With(a.logger, "type", "local", "command", r.LocalCommand)
		err = a.Response.runLocalCommand(localLogger)
		if err != nil {
			level.Error(localLogger).Log("msg", "Failed to run local command", "err", err)
		}
		level.Info(localLogger).Log("msg", "Command completed", "duration", time.Since(start).Seconds())
	}
	if a.Response.SSHCommand != "" {
		sshLogger := log.With(a.logger, "type", "ssh", "user", r.User, "ssh_key", r.SSHKey,
			"ssh_host", r.SSHHost, "command", r.SSHCommand)
		err = a.Response.runSSHCommand(sshLogger)
		if err != nil {
			level.Error(sshLogger).Log("msg", "Failed to run SSH command", "err", err)
		}
		level.Info(sshLogger).Log("msg", "Command completed", "duration", time.Since(start).Seconds())
	}
	return err
}