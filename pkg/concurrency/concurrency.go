package concurrency

import "sync"

// FanIn merges multiple input channels into a single output channel.
//
// # Visualization
//
// K1 ------|
// K2 ------|
// K3 ------|------> K
// K4 ------|
// K5 ------|
//
// All values from K1, K2, K3, K4 and K5
// will be forwarded to the output channel K
func FanIn[K any](done <-chan struct{}, channels ...<-chan K) <-chan K {
	wg := sync.WaitGroup{}
	wg.Add(len(channels))
	output := make(chan K)
	for _, ch := range channels {
		go func(ch <-chan K) {
			defer wg.Done()
			for n := range ch {
				select {
				case <-done:
					return
				case output <- n:
				}
			}
		}(ch)
	}
	go func() {
		wg.Wait()
		close(output)
	}()
	return output
}

// FanOut distributes values from a single input channel K
// to multiple output channels.
//
// Visualization
//
//	|------> K1
//	|------> K2
//
// K ------|------> K3
//
//	|------> K4
//	|------> K5
//
// Each value received from K will be sent to all output channels.
func FanOut[K any](done <-chan struct{}, input <-chan K, n int) []<-chan K {
	outputs := make([]chan K, n)
	for i := range outputs {
		outputs[i] = make(chan K)
	}
	go func() {
		defer CloseAll(outputs...)
		i := 0
		for {
			select {
			case <-done:
				return
			case msg, ok := <-input:
				if !ok {
					return
				}
				select {
				case <-done:
					return
				case outputs[i] <- msg:
				}
				i = (i + 1) % n //round-robin
			}
		}
	}()
	return toReadOnly(outputs)
}

// Broadcast duplicates each value from the input channel K
// into n output channels.
//
// Visualization
//
//	|------> K(1)
//	|------> K(2)
//
// K ------|------> K(3)
//
//	|------> K(4)
//	|------> K(5)
//
// # Every message received from K is delivered to all output channels
//
// Cancellation:
//   - If done is closed, stop broadcasting
//   - If input channel is closed, all outputs are closed
//
// Returns n output channels.
//
// Note:
// Broadcast blocks until each output channel has received the value.
// A slow consumer may delay delivery to other outputs.
func Broadcast[K any](done <-chan struct{}, input <-chan K, n int) []chan K {
	outputs := CreateAll[K](n)
	go func() {
		defer CloseAll(outputs...)
		for {
			select {
			case <-done:
				return
			case msg, ok := <-input:
				if !ok {
					return
				}
				for _, output := range outputs {
					select {
					case <-done:
						return
					case output <- msg:
					}
				}
			}
		}
	}()
	return outputs
}

func CreateAll[K any](n int) []chan K {
	channels := make([]chan K, n)
	for i := range channels {
		channels[i] = make(chan K)
	}
	return channels
}

func CloseAll[K any](channels ...chan K) {
	for _, ch := range channels {
		close(ch)
	}
}

func toReadOnly[K any](outputs []chan K) []<-chan K {
	readOnly := make([]<-chan K, len(outputs))
	for i, ch := range outputs {
		readOnly[i] = ch
	}
	return readOnly
}
