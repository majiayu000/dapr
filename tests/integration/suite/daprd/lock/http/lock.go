/*
Copyright 2024 The Dapr Authors
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at
    http://www.apache.org/licenses/LICENSE-2.0
Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package http

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/client"
	procdaprd "github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(lockTests))
}

type lockTests struct {
	daprd *procdaprd.Daprd
}

// Setup configures a daprd instance with a lock component.
// We use 'lock.redis' as it is a standard component and usually available in integration tests.
func (l *lockTests) Setup(t *testing.T) []framework.Option {
	// Create a resource file for the lock component
	lockComponent := `
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: mylock
spec:
  type: lock.redis
  version: v1
  metadata:
  - name: redisHost
    value: localhost:6379
  - name: redisPassword
    value: ""
`

	l.daprd = procdaprd.New(t,
		procdaprd.WithResourceFiles(lockComponent),
		procdaprd.WithErrorCodeMetrics(t),
	)

	return []framework.Option{
		framework.WithProcesses(l.daprd),
	}
}

func (l *lockTests) Run(t *testing.T, ctx context.Context) {
	l.daprd.WaitUntilRunning(t, ctx)

	httpClient := client.HTTP(t)
	baseURL := fmt.Sprintf("http://localhost:%d/v1.0/lock/mylock", l.daprd.HTTPPort())

	// Helper to make requests
	makeRequest := func(t *testing.T, method string, body string) (*http.Response, string) {
		t.Helper()
		var bodyReader io.Reader
		if body != "" {
			bodyReader = strings.NewReader(body)
		}
		req, err := http.NewRequestWithContext(ctx, method, baseURL, bodyReader)
		require.NoError(t, err)
		req.Header.Set("Content-Type", "application/json")
		resp, err := httpClient.Do(req)
		require.NoError(t, err)
		respBody, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		require.NoError(t, resp.Body.Close())
		return resp, string(respBody)
	}

	t.Run("TryLock success", func(t *testing.T) {
		body := `{"resourceId": "res1", "lockOwner": "owner1", "expiryInSeconds": 60}`
		resp, respBody := makeRequest(t, http.MethodPost, body)
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		assert.JSONEq(t, `{"success":true}`, respBody)
	})

	t.Run("Unlock success", func(t *testing.T) {
		unlockURL := fmt.Sprintf("http://localhost:%d/v1.0/unlock/mylock", l.daprd.HTTPPort())
		body := `{"resourceId": "res1", "lockOwner": "owner1"}`
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, unlockURL, strings.NewReader(body))
		require.NoError(t, err)
		req.Header.Set("Content-Type", "application/json")
		resp, err := httpClient.Do(req)
		require.NoError(t, err)
		assert.Equal(t, http.StatusOK, resp.StatusCode)
	})

	t.Run("Store Not Found", func(t *testing.T) {
		url := fmt.Sprintf("http://localhost:%d/v1.0/lock/nonexistent", l.daprd.HTTPPort())
		body := `{"resourceId": "res1", "lockOwner": "owner1", "expiryInSeconds": 60}`
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, strings.NewReader(body))
		require.NoError(t, err)
		req.Header.Set("Content-Type", "application/json")
		resp, err := httpClient.Do(req)
		require.NoError(t, err)
		respBody, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		require.NoError(t, resp.Body.Close())

		assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
		assert.Contains(t, string(respBody), "ERR_LOCK_STORE_NOT_FOUND")
	})

	t.Run("Resource ID Empty", func(t *testing.T) {
		body := `{"resourceId": "", "lockOwner": "owner1", "expiryInSeconds": 60}`
		resp, respBody := makeRequest(t, http.MethodPost, body)
		assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
		assert.Contains(t, respBody, "ERR_MALFORMED_REQUEST")
		assert.Contains(t, respBody, "ResourceId is empty")
	})

	t.Run("Lock Owner Empty", func(t *testing.T) {
		body := `{"resourceId": "res2", "lockOwner": "", "expiryInSeconds": 60}`
		resp, respBody := makeRequest(t, http.MethodPost, body)
		assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
		assert.Contains(t, respBody, "ERR_MALFORMED_REQUEST")
		assert.Contains(t, respBody, "LockOwner is empty")
	})

	t.Run("Expiry Not Positive", func(t *testing.T) {
		body := `{"resourceId": "res3", "lockOwner": "owner2", "expiryInSeconds": 0}`
		resp, respBody := makeRequest(t, http.MethodPost, body)
		assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
		assert.Contains(t, respBody, "ERR_MALFORMED_REQUEST")
		assert.Contains(t, respBody, "ExpiryInSeconds is not positive")
	})
}
