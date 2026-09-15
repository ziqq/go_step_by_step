package main

import (
	"errors"
	"fmt"
)

var (
	ErrDivisionByZero = errors.New("division by zero")
	ErrInvalidStatus  = errors.New("invalid HTTP status")
	ErrNilLogger      = errors.New("logger is nil")
)

func average(total, count int) (float64, error) {
	if count == 0 {
		return 0, ErrDivisionByZero
	}

	return float64(total) / float64(count), nil
}

func statusCategory(status int) (string, error) {
	if status < 100 || status > 599 {
		return "", fmt.Errorf("%w: %d", ErrInvalidStatus, status)
	}

	switch {
	case status < 200:
		return "informational", nil
	case status < 300:
		return "success", nil
	case status < 400:
		return "redirect", nil
	case status < 500:
		return "client_error", nil
	default:
		return "server_error", nil
	}
}

func inspectStatus(status int, log func(string)) (string, error) {
	if log == nil {
		return "", ErrNilLogger
	}

	log("start")
	defer log("finish")

	return statusCategory(status)
}

func main() {
	events := make([]string, 0, 2)
	category, err := inspectStatus(503, func(event string) {
		events = append(events, event)
	})
	if err != nil {
		fmt.Printf("status inspection failed: %v\n", err)
		return
	}

	result, err := average(15, 2)
	if err != nil {
		fmt.Printf("average failed: %v\n", err)
		return
	}

	fmt.Printf("status category: %s\n", category)
	fmt.Printf("events: %v\n", events)
	fmt.Printf("average: %.2f\n", result)
}
