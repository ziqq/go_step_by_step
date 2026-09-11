package main

import (
	"reflect"
	"testing"
)

func TestSum(t *testing.T) {
	tests := []struct {
		name   string
		values []int
		want   int
	}{
		{name: "nil slice", values: nil, want: 0},
		{name: "empty slice", values: []int{}, want: 0},
		{name: "positive values", values: []int{1, 2, 3}, want: 6},
		{name: "negative values", values: []int{-2, 5, -1}, want: 2},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := sum(test.values); got != test.want {
				t.Fatalf("sum(%v) = %d, want %d", test.values, got, test.want)
			}
		})
	}
}

func TestUniqueMethods(t *testing.T) {
	tests := []struct {
		name    string
		methods []string
		want    []string
	}{
		{name: "nil slice", methods: nil, want: []string{}},
		{
			name:    "preserves first occurrence order",
			methods: []string{"messages.send", "messages.load", "messages.send"},
			want:    []string{"messages.send", "messages.load"},
		},
		{name: "already unique", methods: []string{"a", "b"}, want: []string{"a", "b"}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := uniqueMethods(test.methods); !reflect.DeepEqual(got, test.want) {
				t.Fatalf("uniqueMethods(%v) = %v, want %v", test.methods, got, test.want)
			}
		})
	}
}

func TestCountStatuses(t *testing.T) {
	got := countStatuses([]int{200, 429, 500, 201, 429})
	want := map[int]int{200: 1, 201: 1, 429: 2, 500: 1}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("countStatuses(...) = %v, want %v", got, want)
	}
}
