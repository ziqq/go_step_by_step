package main

import "testing"

func TestAverage(t *testing.T) {
	if got, want := average(12, 3), 4.0; got != want {
		t.Fatalf("average(12, 3) = %v, want %v", got, want)
	}

	if got, want := average(7, 3), 7.0/3.0; got != want {
		t.Fatalf("average(7, 3) = %v, want %v", got, want)
	}

	if got, want := average(7, 0), 0.0; got != want {
		t.Fatalf("average(7, 0) = %v, want %v", got, want)
	}
}

func TestRuneCount(t *testing.T) {
	if got, want := runeCount("Go язык"), 7; got != want {
		t.Fatalf("runeCount() = %d, want %d", got, want)
	}
}

func TestCelsiusToFahrenheit(t *testing.T) {
	if got, want := celsiusToFahrenheit(20), 68.0; got != want {
		t.Fatalf("celsiusToFahrenheit() = %v, want %v", got, want)
	}
}

func TestPercentage(t *testing.T) {
	if got, want := percentage(3, 4), 75.0; got != want {
		t.Fatalf("percentage(3, 4) = %v, want %v", got, want)
	}

	if got, want := percentage(1, 3), 33.33333333333333; got != want {
		t.Fatalf("percentage(1, 3) = %v, want %v", got, want)
	}

	if got, want := percentage(1, 0), 0.0; got != want {
		t.Fatalf("percentage(1, 0) = %v, want %v", got, want)
	}
}