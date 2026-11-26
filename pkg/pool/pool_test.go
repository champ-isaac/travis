package pool

import (
	"bytes"
	"fmt"
	"testing"
)

func TestBytesBufferPool(t *testing.T) {
	bufPool := New(func() *bytes.Buffer {
		return new(bytes.Buffer)
	})
	b := bufPool.Get().(*bytes.Buffer)
	b.Reset()
	b.WriteString("hello world")

	t.Log(b.String())
	bufPool.Put(b)
}

type RequestContext struct {
	ID     string `json:"id"`
	Buffer *bytes.Buffer
}

func TestRequestContextPool(t *testing.T) {
	ctxPool := New(func() *RequestContext {
		return &RequestContext{
			Buffer: new(bytes.Buffer),
		}
	})
	ctx := ctxPool.Get().(*RequestContext)
	ctx.Buffer.Reset()
	ctx.ID = "abc123"
	ctx.Buffer.WriteString("hello world payload")
	ctxPool.Put(ctx)

	reuseCtx := ctxPool.Get().(*RequestContext)
	t.Log(reuseCtx.ID, reuseCtx.Buffer.String())
}

func BenchmarkBytesBufferPool(b *testing.B) {
	bufPool := New(func() *bytes.Buffer {
		return new(bytes.Buffer)
	})
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		bp := bufPool.Get().(*bytes.Buffer)
		bp.WriteString(fmt.Sprintf("hello world id: %d", i))
		bufPool.Put(bp)
	}
	//bufPool.Put(nil)
}
