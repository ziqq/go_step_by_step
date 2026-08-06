package main

import "fmt"

func average(total int, count int) float64 {
	if count == 0 {
		return 0
	}

	return float64(total) / float64(count)
}

func runeCount(text string) int {
	return len([]rune(text))
}

func celsiusToFahrenheit(celsius float64) float64 {
	return celsius*9/5 + 32
}

func percentage(completed, total int) float64 {
	if total == 0 {
		return 0
	}

	return float64(completed) / float64(total) * 100
}

func main() {
	var visits int
	name := "Go"
	active := true

	fmt.Printf("name: %q (%T)\n", name, name)
	fmt.Printf("visits: %d (%T), zero value\n", visits, visits)
	fmt.Printf("active: %t (%T)\n", active, active)
	fmt.Printf("average: %.1f\n", average(7, 3))
	fmt.Printf("bytes and runes in %q: %d and %d\n", "Go язык", len("Go язык"), runeCount("Go язык"))
	fmt.Printf("20 C in Fahrenheit: %.1f\n", celsiusToFahrenheit(20))

	fmt.Printf("percentage: %.1f%%\n", percentage(3, 4))
}
