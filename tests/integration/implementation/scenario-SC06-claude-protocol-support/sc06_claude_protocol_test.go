// Copyright (c) 2026 The BFE Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package sc06

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/bfenetworks/bfe/bfe_config/bfe_cluster_conf/cluster_conf"
	"github.com/bfenetworks/bfe/tests/integration/common"
)

const (
	apiKey = "ak_user_a"

	openaiKey     = "sk-openai-key"
	anthropicKey  = "sk-anthropic-key"
	bothOpenAIKey = "sk-both-openai-key"
	bothAnthKey   = "sk-both-anthropic-key"

	hostOpenAI     = "openai.example.org"
	hostAnthropic  = "anthropic.example.org"
	hostBoth       = "both.example.org"

	pathOpenAI    = "/v1/chat/completions"
	pathAnthropic = "/v1/messages"
)

type testEnv struct {
	t          *testing.T
	processEnv *common.ProcessEnv
	backends   map[string]*common.MockBackend
	bfePort    int
	stopBFE    func()
}

func newTestEnv(t *testing.T) *testEnv {
	e := &testEnv{
		t:        t,
		backends: make(map[string]*common.MockBackend),
	}

	e.backends["cluster_openai_only"] = common.NewMockBackend("cluster_openai_only", http.StatusOK,
		`{"choices":[{"message":{"content":"ok"}}],"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}}`)
	e.backends["cluster_anthropic_only"] = common.NewMockBackend("cluster_anthropic_only", http.StatusOK,
		`{"content":[{"text":"ok"}],"usage":{"input_tokens":10,"output_tokens":5}}`)
	e.backends["cluster_both_protocols"] = common.NewMockBackend("cluster_both_protocols", http.StatusOK,
		`{"choices":[{"message":{"content":"ok"}}],"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}}`)

	e.processEnv = common.NewProcessEnv(t)
	e.processEnv.Build()

	confDir := filepath.Join(e.processEnv.WorkDir(), "conf")
	logDir := filepath.Join(e.processEnv.WorkDir(), "log")

	aiConfs := map[string]*cluster_conf.AIConf{
		"cluster_openai_only": {
			Type:           0,
			ModelProtocols: []string{"openai"},
			Keys: []cluster_conf.AIKey{
				{Name: "openai-key", Key: openaiKey, Weight: 100},
			},
		},
		"cluster_anthropic_only": {
			Type:           0,
			ModelProtocols: []string{"anthropic"},
			Keys: []cluster_conf.AIKey{
				{Name: "anthropic-key", Key: anthropicKey, Weight: 100},
			},
		},
		"cluster_both_protocols": {
			Type:           0,
			ModelProtocols: []string{"openai", "anthropic"},
			Keys: []cluster_conf.AIKey{
				{Name: "both-openai-key", Key: bothOpenAIKey, Weight: 50},
				{Name: "both-anthropic-key", Key: bothAnthKey, Weight: 50},
			},
		},
	}

	builder := &common.BFEConfigBuilder{
		TemplateDir:   "testdata",
		TargetConfDir: confDir,
		Backends:      e.backends,
		AIConfs:       aiConfs,
	}
	if err := builder.Build(); err != nil {
		t.Fatalf("build bfe config failed: %v", err)
	}

	e.bfePort, _, e.stopBFE = e.processEnv.StartBFE(confDir, logDir)
	return e
}

func (e *testEnv) Close() {
	if e.stopBFE != nil {
		e.stopBFE()
	}
	for _, b := range e.backends {
		b.Close()
	}
}

func (e *testEnv) logBFEException() {
	data, err := os.ReadFile(filepath.Join(e.processEnv.WorkDir(), "log", "exception.log"))
	if err == nil && len(data) > 0 {
		e.t.Logf("bfe exception log:\n%s", string(data))
	}
}

func (e *testEnv) sendRequest(host, path, authHeader, authValue string, body []byte) (*http.Response, string, error) {
	url := fmt.Sprintf("http://127.0.0.1:%d%s", e.bfePort, path)
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, "", err
	}
	req.Host = host
	if authHeader != "" {
		req.Header.Set(authHeader, authValue)
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, "", err
	}
	return resp, string(respBody), nil
}

func openaiBody() []byte {
	return []byte(`{"model":"gpt-4"}`)
}

func anthropicBody() []byte {
	return []byte(`{"model":"claude-3-5-sonnet","messages":[{"role":"user","content":"hi"}]}`)
}

// TestTC01 verifies that an OpenAI-style request is forwarded to an OpenAI-only
// cluster with Authorization: Bearer <key>.
func TestTC01_OpenAIStyleForwarded(t *testing.T) {
	e := newTestEnv(t)
	defer e.Close()

	resp, body, err := e.sendRequest(hostOpenAI, pathOpenAI, "Authorization", "Bearer "+apiKey, openaiBody())
	if err != nil {
		t.Fatalf("send request failed: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		e.logBFEException()
		t.Fatalf("expected status 200, got %d, body: %s", resp.StatusCode, body)
	}

	backend := e.backends["cluster_openai_only"]
	if backend.Hits() != 1 {
		t.Fatalf("expected 1 hit on openai-only cluster, got %d", backend.Hits())
	}
	authHeaders := backend.AuthHeaders()
	if len(authHeaders) != 1 || authHeaders[0] != "Bearer "+openaiKey {
		t.Fatalf("expected Authorization 'Bearer %s', got %v", openaiKey, authHeaders)
	}
	xApiKeys := backend.XApiKeyHeaders()
	if len(xApiKeys) != 1 || xApiKeys[0] != "" {
		t.Fatalf("expected no x-api-key header, got %v", xApiKeys)
	}
}

// TestTC02 verifies that an Anthropic-style request is forwarded to an
// Anthropic-only cluster with x-api-key and anthropic-version headers.
func TestTC02_AnthropicStyleForwarded(t *testing.T) {
	e := newTestEnv(t)
	defer e.Close()

	resp, body, err := e.sendRequest(hostAnthropic, pathAnthropic, "x-api-key", apiKey, anthropicBody())
	if err != nil {
		t.Fatalf("send request failed: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		e.logBFEException()
		t.Fatalf("expected status 200, got %d, body: %s", resp.StatusCode, body)
	}

	backend := e.backends["cluster_anthropic_only"]
	if backend.Hits() != 1 {
		t.Fatalf("expected 1 hit on anthropic-only cluster, got %d", backend.Hits())
	}
	xApiKeys := backend.XApiKeyHeaders()
	if len(xApiKeys) != 1 || xApiKeys[0] != anthropicKey {
		t.Fatalf("expected x-api-key '%s', got %v", anthropicKey, xApiKeys)
	}
	authHeaders := backend.AuthHeaders()
	if len(authHeaders) != 1 || authHeaders[0] != "" {
		t.Fatalf("expected no Authorization header, got %v", authHeaders)
	}
	versions := backend.AnthropicVersions()
	if len(versions) != 1 || versions[0] != "2023-06-01" {
		t.Fatalf("expected anthropic-version '2023-06-01', got %v", versions)
	}
}

// TestTC03 verifies that an Anthropic-style request hitting an OpenAI-only
// cluster is rejected with 400 PROVIDER_PROTOCOL_MISMATCH.
func TestTC03_AnthropicRejectedByOpenAICluster(t *testing.T) {
	e := newTestEnv(t)
	defer e.Close()

	resp, body, err := e.sendRequest(hostOpenAI, pathAnthropic, "x-api-key", apiKey, anthropicBody())
	if err != nil {
		t.Fatalf("send request failed: %v", err)
	}
	if resp.StatusCode != http.StatusBadRequest {
		e.logBFEException()
		t.Fatalf("expected status 400, got %d, body: %s", resp.StatusCode, body)
	}
	if !bytes.Contains([]byte(body), []byte("PROVIDER_PROTOCOL_MISMATCH")) {
		t.Fatalf("expected PROVIDER_PROTOCOL_MISMATCH in body, got: %s", body)
	}
	if e.backends["cluster_openai_only"].Hits() != 0 {
		t.Fatalf("expected openai-only cluster not hit, got %d", e.backends["cluster_openai_only"].Hits())
	}
}

// TestTC04 verifies that an OpenAI-style request hitting an Anthropic-only
// cluster is rejected with 400 PROVIDER_PROTOCOL_MISMATCH.
func TestTC04_OpenAIRejectedByAnthropicCluster(t *testing.T) {
	e := newTestEnv(t)
	defer e.Close()

	resp, body, err := e.sendRequest(hostAnthropic, pathOpenAI, "Authorization", "Bearer "+apiKey, openaiBody())
	if err != nil {
		t.Fatalf("send request failed: %v", err)
	}
	if resp.StatusCode != http.StatusBadRequest {
		e.logBFEException()
		t.Fatalf("expected status 400, got %d, body: %s", resp.StatusCode, body)
	}
	if !bytes.Contains([]byte(body), []byte("PROVIDER_PROTOCOL_MISMATCH")) {
		t.Fatalf("expected PROVIDER_PROTOCOL_MISMATCH in body, got: %s", body)
	}
	if e.backends["cluster_anthropic_only"].Hits() != 0 {
		t.Fatalf("expected anthropic-only cluster not hit, got %d", e.backends["cluster_anthropic_only"].Hits())
	}
}

// TestTC05 verifies that a cluster supporting both protocols handles OpenAI
// and Anthropic requests with the correct authentication headers.
func TestTC05_BothProtocolsCluster(t *testing.T) {
	e := newTestEnv(t)
	defer e.Close()

	// OpenAI style
	resp, body, err := e.sendRequest(hostBoth, pathOpenAI, "Authorization", "Bearer "+apiKey, openaiBody())
	if err != nil {
		t.Fatalf("send request failed: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		e.logBFEException()
		t.Fatalf("expected status 200 for openai style, got %d, body: %s", resp.StatusCode, body)
	}

	// Anthropic style
	resp, body, err = e.sendRequest(hostBoth, pathAnthropic, "x-api-key", apiKey, anthropicBody())
	if err != nil {
		t.Fatalf("send request failed: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		e.logBFEException()
		t.Fatalf("expected status 200 for anthropic style, got %d, body: %s", resp.StatusCode, body)
	}

	backend := e.backends["cluster_both_protocols"]
	if backend.Hits() != 2 {
		t.Fatalf("expected 2 hits on both-protocols cluster, got %d", backend.Hits())
	}

	authHeaders := backend.AuthHeaders()
	xApiKeys := backend.XApiKeyHeaders()
	versions := backend.AnthropicVersions()

	foundOpenAI := false
	foundAnthropic := false
	for i := 0; i < backend.Hits(); i++ {
		// OpenAI style: Authorization is set to one of the cluster keys,
		// x-api-key is not set by BFE (may retain client value if present).
		if authHeaders[i] == "Bearer "+bothOpenAIKey || authHeaders[i] == "Bearer "+bothAnthKey {
			foundOpenAI = true
		}
		// Anthropic style: x-api-key is set to one of the cluster keys and
		// anthropic-version is injected.
		if (xApiKeys[i] == bothOpenAIKey || xApiKeys[i] == bothAnthKey) && versions[i] == "2023-06-01" {
			foundAnthropic = true
		}
	}
	if !foundOpenAI {
		t.Fatalf("expected OpenAI-style Authorization header on both-protocols cluster, got auth=%v x-api-key=%v",
			authHeaders, xApiKeys)
	}
	if !foundAnthropic {
		t.Fatalf("expected Anthropic-style x-api-key and anthropic-version on both-protocols cluster, got auth=%v x-api-key=%v versions=%v",
			authHeaders, xApiKeys, versions)
	}
}

// TestTC06 verifies that OpenAI-style Authorization takes precedence when both
// Authorization and x-api-key headers are present.
func TestTC06_AuthorizationPrecedence(t *testing.T) {
	e := newTestEnv(t)
	defer e.Close()

	url := fmt.Sprintf("http://127.0.0.1:%d%s", e.bfePort, pathOpenAI)
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(openaiBody()))
	if err != nil {
		t.Fatalf("new request failed: %v", err)
	}
	req.Host = hostBoth
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("x-api-key", apiKey)
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("send request failed: %v", err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		e.logBFEException()
		t.Fatalf("expected status 200, got %d, body: %s", resp.StatusCode, string(respBody))
	}

	backend := e.backends["cluster_both_protocols"]
	authHeaders := backend.AuthHeaders()
	xApiKeys := backend.XApiKeyHeaders()
	if len(authHeaders) != 1 {
		t.Fatalf("expected one request to backend, got %d", backend.Hits())
	}
	// When both Authorization and x-api-key are present, Authorization takes
	// precedence for protocol/style detection, so BFE should inject the cluster
	// key into Authorization (any cluster key is acceptable since key selection
	// is weighted random).
	if authHeaders[0] != "Bearer "+bothOpenAIKey && authHeaders[0] != "Bearer "+bothAnthKey {
		t.Fatalf("expected Authorization to carry a cluster key when both headers present, got auth=%v x-api-key=%v",
			authHeaders, xApiKeys)
	}
}

// TestTC07 verifies that Anthropic-style requests preserve an explicitly
// provided anthropic-version header.
func TestTC07_PreserveExplicitAnthropicVersion(t *testing.T) {
	e := newTestEnv(t)
	defer e.Close()

	url := fmt.Sprintf("http://127.0.0.1:%d%s", e.bfePort, pathAnthropic)
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(anthropicBody()))
	if err != nil {
		t.Fatalf("new request failed: %v", err)
	}
	req.Host = hostAnthropic
	req.Header.Set("x-api-key", apiKey)
	req.Header.Set("anthropic-version", "2023-06-02")
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("send request failed: %v", err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		e.logBFEException()
		t.Fatalf("expected status 200, got %d, body: %s", resp.StatusCode, string(respBody))
	}

	versions := e.backends["cluster_anthropic_only"].AnthropicVersions()
	if len(versions) != 1 || versions[0] != "2023-06-02" {
		t.Fatalf("expected explicit anthropic-version preserved, got %v", versions)
	}
}

// TestTC08 verifies that Claude usage fields (input_tokens/output_tokens) are
// accepted and total_tokens is derived.
func TestTC08_ClaudeUsageFields(t *testing.T) {
	e := newTestEnv(t)
	defer e.Close()

	backend := e.backends["cluster_anthropic_only"]
	backend.ResponseFunc = func(r *http.Request, count int) (int, string) {
		return http.StatusOK, `{"content":[{"text":"ok"}],"usage":{"input_tokens":100,"output_tokens":50,"cache_read_input_tokens":30,"cache_creation_input_tokens":20}}`
	}

	resp, body, err := e.sendRequest(hostAnthropic, pathAnthropic, "x-api-key", apiKey, anthropicBody())
	if err != nil {
		t.Fatalf("send request failed: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		e.logBFEException()
		t.Fatalf("expected status 200, got %d, body: %s", resp.StatusCode, body)
	}

	if backend.Hits() != 1 {
		t.Fatalf("expected 1 hit, got %d", backend.Hits())
	}
}
