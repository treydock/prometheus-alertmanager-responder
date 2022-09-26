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
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"net"
	"os"
	"os/exec"
	"time"

	"github.com/go-kit/kit/log"
	"github.com/go-kit/kit/log/level"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/treydock/alertmanager-command-responder/internal/metrics"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

var (
	execCommand = exec.CommandContext
)

func (r *AlertResponse) runLocalCommand(logger log.Logger) error {
	level.Info(logger).Log("msg", "Running local command")
	errorsTotalLabels := prometheus.Labels{"type": "local"}
	var stdout, stderr bytes.Buffer
	ctx, cancel := context.WithTimeout(context.Background(), r.LocalCommandTimeout)
	defer cancel()
	cmd := execCommand(ctx, r.LocalCommand)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if ctx.Err() == context.DeadlineExceeded {
		level.Error(logger).Log("msg", "Local command timed out")
		metrics.CommandErrorsTotal.With(errorsTotalLabels).Inc()
		return fmt.Errorf("Local command timed out: %s", r.LocalCommand)
	} else if err != nil {
		level.Error(logger).Log("msg", "Error executing command", "err", err)
		metrics.CommandErrorsTotal.With(errorsTotalLabels).Inc()
		return err
	}
	level.Info(logger).Log("msg", "Local command completed", "out", stdout.String(), "err", stderr.String())
	return nil
}

func (r *AlertResponse) runSSHCommand(logger log.Logger) error {
	level.Info(logger).Log("msg", "Running SSH command")
	errorsTotalLabels := prometheus.Labels{"type": "ssh"}
	c1 := make(chan int, 1)
	var auth ssh.AuthMethod
	var err, sessionerror, commanderror error
	var stdout, stderr bytes.Buffer
	timeout := false

	if r.SSHKey != "" {
		auth, err = getPrivateKeyAuth(r.SSHKey)
		if err != nil {
			level.Error(logger).Log("msg", "Error setting up private key auth", "err", err)
			return err
		}
	} else if r.SSHPassword != "" {
		auth = ssh.Password(r.SSHPassword)
	}
	sshConfig := &ssh.ClientConfig{
		User:              r.User,
		Auth:              []ssh.AuthMethod{auth},
		HostKeyCallback:   hostKeyCallback(r.SSHKnownHosts, logger),
		HostKeyAlgorithms: r.SSHHostKeyAlgorithms,
		Timeout:           r.SSHConnectionTimeout,
	}
	connection, err := ssh.Dial("tcp", r.SSHHost, sshConfig)
	if err != nil {
		level.Error(logger).Log("msg", "Failed to establish SSH connection", "err", err)
		metrics.CommandErrorsTotal.With(errorsTotalLabels).Inc()
		return err
	}
	defer connection.Close()

	go func(conn *ssh.Client) {
		var session *ssh.Session
		session, sessionerror = conn.NewSession()
		if sessionerror != nil {
			return
		}
		session.Stdout = &stdout
		session.Stderr = &stderr
		commanderror = session.Run(r.SSHCommand)
		if commanderror != nil {
			return
		}
		if !timeout {
			c1 <- 1
		}
	}(connection)

	select {
	case <-c1:
	case <-time.After(r.SSHCommandTimeout):
		timeout = true
		close(c1)
		level.Error(logger).Log("msg", "Timeout executing SSH command")
		metrics.CommandErrorsTotal.With(errorsTotalLabels).Inc()
		return fmt.Errorf("Timeout executing SSH command: %s", r.SSHCommand)
	}
	close(c1)

	if sessionerror != nil {
		level.Error(logger).Log("msg", "Failed to establish SSH session", "err", sessionerror)
		metrics.CommandErrorsTotal.With(errorsTotalLabels).Inc()
		return sessionerror
	}
	if commanderror != nil {
		level.Error(logger).Log("msg", "Failed to run SSH command", "err", commanderror)
		metrics.CommandErrorsTotal.With(errorsTotalLabels).Inc()
		return commanderror
	}
	level.Info(logger).Log("msg", "SSH command completed", "out", stdout.String(), "err", stderr.String())
	return nil
}

func getPrivateKeyAuth(privatekey string) (ssh.AuthMethod, error) {
	buffer, err := os.ReadFile(privatekey)
	if err != nil {
		return nil, err
	}
	key, err := ssh.ParsePrivateKey(buffer)
	if err != nil {
		return nil, err
	}
	return ssh.PublicKeys(key), nil
}

func hostKeyCallback(knownHosts string, logger log.Logger) ssh.HostKeyCallback {
	return func(hostname string, remote net.Addr, key ssh.PublicKey) error {
		var hostKeyCallback ssh.HostKeyCallback
		var err error
		if knownHosts != "" {
			publicKey := base64.StdEncoding.EncodeToString(key.Marshal())
			level.Debug(logger).Log("msg", "Verify SSH known hosts", "hostname", hostname, "remote", remote.String(), "key", publicKey)
			hostKeyCallback, err = knownhosts.New(knownHosts)
			if err != nil {
				level.Error(logger).Log("msg", "Error creating hostkeycallback function", "err", err)
				metrics.CommandErrorsTotal.With(prometheus.Labels{"type": "ssh"}).Inc()
				return err
			}
		} else {
			hostKeyCallback = ssh.InsecureIgnoreHostKey()
		}
		return hostKeyCallback(hostname, remote, key)
	}
}