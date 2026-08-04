package main

import "testing"

func TestLessonMessage(t *testing.T) {
	got := lessonMessage()
	want := "Go step by step: start here"

	if got != want {
		t.Fatalf("lessonMessage() = %q, want %q", got, want)
	}
}
