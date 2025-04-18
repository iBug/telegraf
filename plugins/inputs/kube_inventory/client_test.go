package kube_inventory

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/influxdata/telegraf/plugins/common/tls"
)

type mockHandler struct {
	responseMap map[string]interface{}
}

func toStrPtr(s string) *string {
	return &s
}

func toInt32Ptr(i int32) *int32 {
	return &i
}

func toBoolPtr(b bool) *bool {
	return &b
}

func TestNewClient(t *testing.T) {
	_, err := newClient("https://127.0.0.1:443/", "default", "", "abc123", time.Second, tls.ClientConfig{})
	require.NoErrorf(t, err, "Failed to create new client: %v", err)

	_, err = newClient("https://127.0.0.1:443/", "default", "nonexistantFile", "", time.Second, tls.ClientConfig{})
	require.Errorf(t, err, "Failed to read token file \"file\": open file: no such file or directory: %v", err)
}
