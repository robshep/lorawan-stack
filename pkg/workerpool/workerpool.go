// Copyright © 2021 The Things Network Foundation, The Things Industries B.V.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package workerpool

import (
	"context"
	"sync/atomic"
	"time"

	"go.thethings.network/lorawan-stack/v3/pkg/component"
	"go.thethings.network/lorawan-stack/v3/pkg/errors"
)

const (
	// defaultWorkerIdleTimeout is the duration after which an idle worker stops to save resources.
	defaultWorkerIdleTimeout = (1 << 7) * time.Millisecond
	// defaultWorkerBusyTimeout is the duration after which a message is dropped if all workers are busy.
	defaultWorkerBusyTimeout = (1 << 6) * time.Millisecond
)

// Component contains a minimal component.Component definition.
type Component interface {
	StartTask(*component.TaskConfig)
	FromRequestContext(context.Context) context.Context
}

// Handler is a function that processes items published to the worker pool.
type Handler func(ctx context.Context, item interface{})

// HandlerFactory is a function that creates a Handler.
type HandlerFactory func() (Handler, error)

// StaticHandlerFactory creates a HandlerFactory that always returns the same handler.
func StaticHandlerFactory(f Handler) HandlerFactory {
	return func() (Handler, error) {
		return f, nil
	}
}

// Config is the configuration of the worker pool.
type Config struct {
	Component
	context.Context                  // The base context of the pool.
	Name              string         // The name of the pool.
	CreateHandler     HandlerFactory // The function that creates handlers.
	MinWorkers        int            // The minimum number of workers in the pool.
	MaxWorkers        int            // The maximum number of workers in the pool.
	QueueSize         int            // The size of the work queue.
	WorkerIdleTimeout time.Duration  // The maximum amount of time a worker will stay idle before closing.
	WorkerBusyTimeout time.Duration  // The maximum amount of time a publisher will wait before dropping the message.
}

// WorkerPool is a dynamic pool of workers to which work items can be published.
// The workers are created on demand and live as long as work is available.
type WorkerPool interface {
	// Publish publishes an item to the worker pool to be processed.
	// Publish does not block indefinitely, and may spawn a worker
	// in order to fullfil the work load.
	Publish(ctx context.Context, item interface{}) error
}

type contextualItem struct {
	ctx  context.Context
	item interface{}
}

type workerPool struct {
	Config
	q       chan *contextualItem
	workers int32
}

func (wp *workerPool) workerBody(handler Handler) func(context.Context) error {
	worker := func(ctx context.Context) error {
		var decremented bool
		defer func() {
			if !decremented {
				atomic.AddInt32(&wp.workers, -1)
			}
		}()

		registerWorkerStarted(wp.Config, wp.Name)
		defer registerWorkerStopped(wp.Context, wp.Name)

		for {
			select {
			case <-wp.Done():
				return wp.Err()

			case <-ctx.Done():
				return ctx.Err()

			case <-time.After(wp.WorkerIdleTimeout):
				if decrementIfGreaterThan(&wp.workers, int32(wp.MinWorkers)) {
					decremented = true
					return nil
				}

			case item := <-wp.q:
				registerWorkDequeued(ctx, wp.Name)
				handler(item.ctx, item.item)
			}
		}
	}
	return worker
}

func (wp *workerPool) spawnWorker() error {
	handler, err := wp.CreateHandler()
	if err != nil {
		return err
	}

	if !incrementIfSmallerThan(&wp.workers, int32(wp.MaxWorkers)) {
		return nil
	}

	wp.StartTask(&component.TaskConfig{
		Context: wp.Context,
		ID:      wp.Name,
		Func:    wp.workerBody(handler),
		Restart: component.TaskRestartNever,
		Backoff: component.DefaultTaskBackoffConfig,
	})

	return nil
}

var errPoolFull = errors.DefineResourceExhausted("pool_full", "the worker pool is full")

// Publish implements WorkerPool.
func (wp *workerPool) Publish(ctx context.Context, item interface{}) error {
	it := &contextualItem{
		ctx:  wp.FromRequestContext(ctx),
		item: item,
	}

	select {
	case <-wp.Done():
		return wp.Err()

	case <-ctx.Done():
		return ctx.Err()

	case wp.q <- it:
		registerWorkEnqueued(ctx, wp.Name)
		return nil

	default:
		if err := wp.spawnWorker(); err != nil {
			return err
		}

		select {
		case <-wp.Done():
			return wp.Err()

		case <-ctx.Done():
			return ctx.Err()

		case wp.q <- it:
			registerWorkEnqueued(ctx, wp.Name)
			return nil

		case <-time.After(wp.WorkerBusyTimeout):
			registerWorkDropped(ctx, wp.Name)
			return errPoolFull.New()
		}
	}
}

// NewWorkerPool creates a new WorkerPool with the provided configuration.
func NewWorkerPool(cfg Config) (WorkerPool, error) {
	if cfg.WorkerBusyTimeout == 0 {
		cfg.WorkerBusyTimeout = defaultWorkerBusyTimeout
	}
	if cfg.WorkerIdleTimeout == 0 {
		cfg.WorkerIdleTimeout = defaultWorkerIdleTimeout
	}
	if cfg.MinWorkers <= 0 {
		cfg.MinWorkers = 1
	}
	if cfg.MaxWorkers <= 0 {
		cfg.MaxWorkers = 1
	}
	if cfg.QueueSize < 0 {
		cfg.QueueSize = 0
	}
	if cfg.MinWorkers > cfg.MaxWorkers {
		cfg.MaxWorkers = cfg.MinWorkers
	}

	wp := &workerPool{
		Config: cfg,
		q:      make(chan *contextualItem, cfg.QueueSize),
	}

	for i := 0; i < wp.MinWorkers; i++ {
		if err := wp.spawnWorker(); err != nil {
			return nil, err
		}
	}

	return wp, nil
}

func incrementIfSmallerThan(i *int32, max int32) bool {
	for v := atomic.LoadInt32(i); v < max; v = atomic.LoadInt32(i) {
		if atomic.CompareAndSwapInt32(i, v, v+1) {
			return true
		}
	}
	return false
}

func decrementIfGreaterThan(i *int32, min int32) bool {
	for v := atomic.LoadInt32(i); v > min; v = atomic.LoadInt32(i) {
		if atomic.CompareAndSwapInt32(i, v, v-1) {
			return true
		}
	}
	return false
}
