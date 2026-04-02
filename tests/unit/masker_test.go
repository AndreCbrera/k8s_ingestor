package masker_test

import (
	"strings"
	"testing"

	"k8s_ingestor/internal/masker"
)

func TestMasker_Mask(t *testing.T) {
	m := masker.New(true)

	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "email",
			input:    "Send to user@example.com please",
			expected: "Send to [EMAIL] please",
		},
		{
			name:     "ip_address",
			input:    "Connection from 192.168.1.1",
			expected: "Connection from [IP]",
		},
		{
			name:     "password",
			input:    "password=secret123",
			expected: "[PASSWORD]",
		},
		{
			name:     "api_key",
			input:    "api_key=abc123xyz",
			expected: "[API_KEY]",
		},
		{
			name:     "no_sensitive",
			input:    "Normal log message",
			expected: "Normal log message",
		},
		{
			name:     "multiple",
			input:    "Email: test@test.com IP: 10.0.0.1",
			expected: "Email: [EMAIL] IP: [IP]",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := m.Mask(tt.input)
			if result != tt.expected {
				t.Errorf("Mask() = %q, want %q", result, tt.expected)
			}
		})
	}
}

func TestMasker_Empty(t *testing.T) {
	m := masker.New(true)

	result := m.Mask("")
	if result != "" {
		t.Errorf("Mask('') = %q, want empty string", result)
	}
}

func TestMasker_Disabled(t *testing.T) {
	m := masker.New(false)

	input := "test@test.com 192.168.1.1 password=secret"
	result := m.Mask(input)

	if result != input {
		t.Errorf("Mask() with disabled masker should return input unchanged")
	}
}

func TestMasker_AddPattern(t *testing.T) {
	m := masker.New(false)
	m.AddPattern("test", `test-\d+`, "[TEST_PATTERN]")

	input := "test-123 matched"
	result := m.Mask(input)

	if !strings.Contains(result, "[TEST_PATTERN]") {
		t.Errorf("Mask() should mask custom pattern, got %q", result)
	}
}

func TestMaskHeaders(t *testing.T) {
	headers := map[string][]string{
		"Content-Type":  {"application/json"},
		"Authorization": {"Bearer token123"},
		"X-API-Key":     {"secret-key"},
		"X-Request-ID":  {"req-123"},
	}

	result := masker.MaskHeaders(headers)

	if result["Content-Type"][0] != "application/json" {
		t.Error("Content-Type should not be masked")
	}

	if result["Authorization"][0] != "[MASKED]" {
		t.Error("Authorization should be masked")
	}

	if result["X-API-Key"][0] != "[MASKED]" {
		t.Error("X-API-Key should be masked")
	}

	if result["X-Request-ID"][0] != "req-123" {
		t.Error("X-Request-ID should not be masked")
	}
}

func BenchmarkMasker_Mask(b *testing.B) {
	m := masker.New(true)
	input := "Email: test@test.com IP: 192.168.1.1 password=secret123 api_key=key123"

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		m.Mask(input)
	}
}
