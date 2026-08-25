package routeoptions

import "sync"

type Options struct {
	HasRetryLimit bool
	RetryLimit    uint64
}

var Pool = sync.Pool{
	New: func() any {
		return newOptions()
	},
}

func newOptions() *Options {
	return &Options{
		HasRetryLimit: false,
		RetryLimit:    0,
	}
}

func (o *Options) Reset() {
	o.HasRetryLimit = false
	o.RetryLimit = 0
}

func (o *Options) SetRetryLimit(limit uint64) {
	o.HasRetryLimit = true
	o.RetryLimit = limit
}

func GetOptions() *Options {
	opts := Pool.Get().(*Options)
	opts.Reset()
	return opts
}

func PutOptions(opts *Options) {
	opts.Reset()
	Pool.Put(opts)
}
