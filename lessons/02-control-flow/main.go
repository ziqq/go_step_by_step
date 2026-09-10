package main

import "fmt"

func isSuccessStatus(status int) bool {
	return status >= 200 && status < 300
}

func isRetryableStatus(status int) bool {
	return status == 408 || status == 429 || status >= 500 && status <= 599
}

func requestDecision(status int) string {
	if success := isSuccessStatus(status); success {
		return "success"
	}

	if isRetryableStatus(status) {
		return "retry"
	}

	return "stop"
}

func retryDelaySeconds(attempt int) int {
	if attempt <= 0 {
		return 0
	}

	delay := 1
	for i := 1; i < attempt; i++ {
		delay *= 2
	}

	return delay
}

func sumFirst(n int) int {
	if n <= 0 {
		return 0
	}

	total := 0
	for i := 1; i <= n; i++ {
		total += i
	}

	return total
}

func main() {
	status := 503

	fmt.Printf("status %d: %s\n", status, requestDecision(status))
	fmt.Printf("retry delay for attempt 3: %ds\n", retryDelaySeconds(3))
	fmt.Printf("sum of first 5 numbers: %d\n", sumFirst(5))
}
