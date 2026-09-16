package main

import "fmt"

func isSuccessStatus(status int) bool {
	return status >= 200 && status < 300
}

func isRetryableStatus(status int) bool {
	return status == 408 || status == 429 || status >= 500 && status <= 599
}

// 5.
// success виден только в нутри функции requestDecision
// В не обязательном иницилизаторе область видимости
// переменной success заканчивается вместе с блоком if
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

// 1.
// Добавьте isValidPort(port int) bool.
// Возвращайте true только для портов от 1 до 65535.
// Проверьте обе границы и недопустимые значения.
func isValidPort(port int) bool {
	if port > 1 && port <= 65535 {
		return true
	}
	return false
}

// 2.
// Добавьте clamp(value, minimum, maximum int) int.
// Возвращайте ближайшую границу, если value выходит за диапазон.
// Зафиксируйте в документации предположение minimum <= maximum. (Это как?)
func clamp(value, minimum, maximum int) int {
	// if (minimum > maximum)
	if value <= maximum && value >= minimum  {
		return value
	} else if value < minimum {
		return minimum
	} else if value > maximum {
		return maximum
	}
	return 0
}

// 3.
// Добавьте isEven(value int) bool, используя оператор остатка.
// В тестах добавьте отрицательное чётное число.
func isEven(value int) bool {
	return value % 2 == 0
}

// 4.
// Добавьте countDown(start int) []int, возвращающую значения от start до 0.
// Не записывайте каждое значение вручную — используйте цикл.
// Тип возвращаемого среза является предварительным знаком следующего урока.
func coundDown(start int) []int {
	var result []int
	for i := start; i >= 0; i-- {
		result = append(result, i)
	}
	return result
}


func main() {
	status := 503

	fmt.Printf("status %d: %s\n", status, requestDecision(status))
	fmt.Printf("retry delay for attempt 3: %ds\n", retryDelaySeconds(3))
	fmt.Printf("sum of first 5 numbers: %d\n", sumFirst(5))

	fmt.Printf("\nInvariant: %t ", isValidPort(0))
	fmt.Printf("\nValid port: %t ", isValidPort(32))
	fmt.Printf("\nNot valid port: %t ", isValidPort(65536))

	fmt.Printf("\nReturn value: %d ", clamp(329, 100, 500))
	fmt.Printf("\nReturn minimum: %d ", clamp(90, 100, 500))
	fmt.Printf("\nReturn maximum: %d ", clamp(500, 100, 500))

	fmt.Printf("\nIs even: %t ", isEven(2))
	fmt.Printf("\nIs not even: %t ", isEven(3))


	fmt.Printf("\nCound down: %v ", coundDown(8))
}
