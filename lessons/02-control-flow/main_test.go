package main

import "testing"

func TestIsSuccessStatus(t *testing.T) {
	tests := []struct {
		name   string
		status int
		want   bool
	}{
		{name: "lower boundary", status: 200, want: true},
		{name: "upper boundary", status: 299, want: true},
		{name: "redirect", status: 302, want: false},
		{name: "client error", status: 404, want: false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := isSuccessStatus(test.status); got != test.want {
				t.Fatalf("isSuccessStatus(%d) = %t, want %t", test.status, got, test.want)
			}
		})
	}
}

func TestIsRetryableStatus(t *testing.T) {
	tests := []struct {
		name   string
		status int
		want   bool
	}{
		{name: "request timeout", status: 408, want: true},
		{name: "rate limit", status: 429, want: true},
		{name: "server error", status: 500, want: true},
		{name: "last server error", status: 599, want: true},
		{name: "not found", status: 404, want: false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := isRetryableStatus(test.status); got != test.want {
				t.Fatalf("isRetryableStatus(%d) = %t, want %t", test.status, got, test.want)
			}
		})
	}
}

func TestRequestDecision(t *testing.T) {
	tests := []struct {
		status int
		want   string
	}{
		{status: 201, want: "success"},
		{status: 429, want: "retry"},
		{status: 403, want: "stop"},
	}

	for _, test := range tests {
		if got := requestDecision(test.status); got != test.want {
			t.Fatalf("requestDecision(%d) = %q, want %q", test.status, got, test.want)
		}
	}
}

func TestRetryDelaySeconds(t *testing.T) {
	tests := []struct {
		attempt int
		want    int
	}{
		{attempt: -1, want: 0},
		{attempt: 0, want: 0},
		{attempt: 1, want: 1},
		{attempt: 2, want: 2},
		{attempt: 4, want: 8},
	}

	for _, test := range tests {
		if got := retryDelaySeconds(test.attempt); got != test.want {
			t.Fatalf("retryDelaySeconds(%d) = %d, want %d", test.attempt, got, test.want)
		}
	}
}

func TestSumFirst(t *testing.T) {
	tests := []struct {
		n    int
		want int
	}{
		{n: -2, want: 0},
		{n: 0, want: 0},
		{n: 1, want: 1},
		{n: 5, want: 15},
	}

	for _, test := range tests {
		if got := sumFirst(test.n); got != test.want {
			t.Fatalf("sumFirst(%d) = %d, want %d", test.n, got, test.want)
		}
	}
}
