package forwarder

import "sync"

const copyBufferSize = 32 * 1024

type reverseProxyBufferPool struct {
	pool sync.Pool
}

func newReverseProxyBufferPool() *reverseProxyBufferPool {
	return &reverseProxyBufferPool{
		pool: sync.Pool{
			New: func() any {
				b := make([]byte, copyBufferSize)
				return &b
			},
		},
	}
}

func (p *reverseProxyBufferPool) Get() []byte {
	return *(p.pool.Get().(*[]byte))
}

func (p *reverseProxyBufferPool) Put(b []byte) {
	p.pool.Put(&b)
}

var sharedReverseProxyBufferPool = newReverseProxyBufferPool()
