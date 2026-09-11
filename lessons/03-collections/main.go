package main

import "fmt"

func sum(values []int) int {
	total := 0
	for _, value := range values {
		total += value
	}

	return total
}

func uniqueMethods(methods []string) []string {
	seen := make(map[string]struct{}, len(methods))
	unique := make([]string, 0, len(methods))

	for _, method := range methods {
		if _, exists := seen[method]; exists {
			continue
		}

		seen[method] = struct{}{}
		unique = append(unique, method)
	}

	return unique
}

func countStatuses(statuses []int) map[int]int {
	counts := make(map[int]int)

	for _, status := range statuses {
		counts[status]++
	}

	return counts
}

func main() {
	statuses := []int{200, 429, 500, 201, 429}
	methods := []string{"messages.send", "messages.load", "messages.send"}

	fmt.Printf("status count: %d\n", len(statuses))
	fmt.Printf("status sum: %d\n", sum(statuses))
	fmt.Printf("unique methods: %v\n", uniqueMethods(methods))
	fmt.Printf("status frequencies: %v\n", countStatuses(statuses))
}
