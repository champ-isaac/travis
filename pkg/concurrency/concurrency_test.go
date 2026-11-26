package concurrency

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestFanIn(t *testing.T) {
	done := make(chan struct{})
	defer close(done)

	ch1 := make(chan int)
	ch2 := make(chan int)

	output := FanIn(done, ch1, ch2)
	go func() {
		defer close(ch1)
		defer close(ch2)
		ch1 <- 1
		ch2 <- 2
		ch1 <- 3
		ch2 <- 4
		ch1 <- 5
	}()
	var results []int
	for v := range output {
		results = append(results, v)
	}
	assert.Equal(t, 5, len(results))
	expected := map[int]bool{1: true, 2: true, 3: true, 4: true, 5: true}
	for _, result := range results {
		if !expected[result] {
			t.Errorf("result %d not expected", result)
		}
	}
}

func TestBroadcast(t *testing.T) {
	done := make(chan struct{})
	defer close(done)
	input := make(chan int)
	outputs := Broadcast(done, input, 10)

	go func() {
		defer close(input)
		for i := 0; i < 10; i++ {
			input <- i
		}
	}()
	var wg sync.WaitGroup
	results := make([][]int, len(outputs))
	for i, ch := range outputs {
		wg.Add(1)
		go func(i int, ch <-chan int) {
			defer wg.Done()
			for v := range ch {
				results[i] = append(results[i], v)
			}
		}(i, ch)
	}
	wg.Wait()
	for i, r := range results {
		for j, v := range r {
			t.Logf("[%d][%d]%d", i, j, v)
		}
	}
}

func TestBroadcast_DoneCancellation(t *testing.T) {
	done := make(chan struct{})
	input := make(chan int, 1)
	outputs := Broadcast(done, input, 10)

	close(done)
	input <- 42 // should not block
	close(input)

	time.Sleep(50 * time.Millisecond)

	for i, ch := range outputs {
		select {
		case v, ok := <-ch:
			t.Logf("[%d]%d: %v", i, v, ok)
			if ok { // should not be true
				t.Errorf("expected outputs[%d]%d to be closed after done", i, v)
			}
		case <-time.After(100 * time.Millisecond):
			t.Errorf("timeout waiting for outputs[%d] to close after done", i)
		}
	}
}

func TestFanOut_Distribution(t *testing.T) {
	done := make(chan struct{})
	defer close(done)

	input := make(chan int)
	outputs := FanOut(done, input, 10)

	go func() {
		defer close(input)
		for i := 0; i < 100; i++ {
			input <- i
		}
	}()
	results := make([][]int, len(outputs))
	var wg sync.WaitGroup
	for i, ch := range outputs {
		wg.Add(1)
		go func(i int, ch <-chan int) {
			defer wg.Done()
			for v := range ch {
				results[i] = append(results[i], v)
			}
		}(i, ch)
	}
	wg.Wait()
	for i, r := range results {
		for j, v := range r {
			t.Logf("[%d][%d]%d", i, j, v)
		}
	}
	//check no duplicate
	seen := make(map[int]int)
	for _, r := range results {
		for _, v := range r {
			seen[v]++
		}
	}
	for k, count := range seen {
		if count != 1 {
			t.Errorf("message %d appears %d times, expected once", k, count)
		}
	}
}

func TestFanOut_CancelClosure(t *testing.T) {
	done := make(chan struct{})
	input := make(chan int)
	outputs := FanOut(done, input, 10)

	close(input)

	for _, ch := range outputs {
		select {
		case _, ok := <-ch:
			assert.False(t, ok)
		case <-time.After(100 * time.Millisecond):
			t.Errorf("timeout waiting for outputs[%d] to close after done", ch)
		}
	}
}

func TestFanOut_DoneCancellation(t *testing.T) {
	done := make(chan struct{})
	input := make(chan int)
	outputs := FanOut(done, input, 10)
	close(done) // cancel early

	// should not block sending on input (goroutine ends)
	select {
	case input <- 42:
		t.Log("input send did not block after done")
	case <-time.After(100 * time.Millisecond):
		t.Log("input send blocked after done")
	}
	close(input)

	for _, ch := range outputs {
		select {
		case _, ok := <-ch:
			assert.False(t, ok)
		case <-time.After(100 * time.Millisecond):
			t.Error("timeout waiting for output to close after done")
		}
	}
}
