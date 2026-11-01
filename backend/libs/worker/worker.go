package worker

import (
	"context"
	"errors"
	"fmt"
	"github.com/artificialflow/artificialflow/backend/libs/model"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

type JobHandler func(ctx context.Context, job model.Job) (map[string]any, error)

type WorkerConfig struct {
	JobType           string
	WorkerName        string
	MaxJobs           int
	ActivateTimeout   time.Duration
	LockDuration      time.Duration
	LockRenewInterval time.Duration
	ErrorBackoff      time.Duration
	Handler           JobHandler
	OnError           func(error)
}

type Worker struct {
	client            *Client
	jobType           string
	workerName        string
	maxJobs           int
	activateTimeout   time.Duration
	lockDuration      time.Duration
	lockRenewInterval time.Duration
	errorBackoff      time.Duration
	handler           JobHandler
	onError           func(error)
}

func NewWorker(client *Client, cfg WorkerConfig) (*Worker, error) {
	if client == nil {
		return nil, fmt.Errorf("client is required")
	}
	if cfg.WorkerName == "" {
		return nil, fmt.Errorf("worker name is required")
	}
	if cfg.Handler == nil {
		return nil, fmt.Errorf("handler is required")
	}
	if cfg.MaxJobs <= 0 {
		cfg.MaxJobs = defaultMaxJobs
	}
	if cfg.ActivateTimeout <= 0 {
		cfg.ActivateTimeout = defaultActivateTimeout
	}
	if cfg.LockDuration <= 0 {
		cfg.LockDuration = defaultLockDuration
	}
	if cfg.LockRenewInterval < 0 {
		return nil, fmt.Errorf("lock renew interval must be non-negative")
	}
	if cfg.ErrorBackoff < 0 {
		cfg.ErrorBackoff = 0
	}

	return &Worker{
		client:            client,
		jobType:           cfg.JobType,
		workerName:        cfg.WorkerName,
		maxJobs:           cfg.MaxJobs,
		activateTimeout:   cfg.ActivateTimeout,
		lockDuration:      cfg.LockDuration,
		lockRenewInterval: cfg.LockRenewInterval,
		errorBackoff:      cfg.ErrorBackoff,
		handler:           cfg.Handler,
		onError:           cfg.OnError,
	}, nil
}

func (w *Worker) Run(ctx context.Context) error {
	for {
		if ctx.Err() != nil {
			return nil
		}

		err := w.RunOnce(ctx)
		if err == nil {
			continue
		}

		if ctx.Err() != nil {
			return nil
		}

		if w.onError != nil {
			w.onError(err)
		}

		if w.errorBackoff > 0 {
			timer := time.NewTimer(w.errorBackoff)
			select {
			case <-ctx.Done():
				timer.Stop()
				return nil
			case <-timer.C:
			}
		}
	}
}

func (w *Worker) RunOnce(ctx context.Context) error {
	jobs, err := w.client.ActivateJobs(ctx, ActivateJobsRequest{
		Type:           w.jobType,
		Worker:         w.workerName,
		MaxJobs:        w.maxJobs,
		TimeoutMs:      int(w.activateTimeout / time.Millisecond),
		LockDurationMs: int(w.lockDuration / time.Millisecond),
	})
	if err != nil {
		return err
	}

	for _, job := range jobs {
		if err := w.processJob(ctx, job); err != nil {
			return err
		}
	}

	return nil
}

func (w *Worker) processJob(ctx context.Context, job model.Job) error {
	jobCtx, span := otel.Tracer("artificialflow/worker").Start(ctx, "worker.process",
		trace.WithSpanKind(trace.SpanKindConsumer),
		trace.WithAttributes(attribute.String("artificialflow.job.type", w.jobType)))
	defer span.End()
	stopLockRenewer := func() error { return nil }

	if w.lockRenewInterval > 0 {
		renewCtx, cancelRenew := context.WithCancel(jobCtx)
		jobCtx = renewCtx
		stopLockRenewer = w.startLockRenewer(renewCtx, cancelRenew, job)
	}

	variables, handlerErr := w.handler(jobCtx, job)
	lockRenewErr := stopLockRenewer()
	if lockRenewErr != nil {
		span.RecordError(lockRenewErr)
		span.SetStatus(codes.Error, "lock renewal failed")
		return lockRenewErr
	}

	if handlerErr != nil {
		span.RecordError(handlerErr)
		span.SetStatus(codes.Error, "job handler failed")
		if errors.Is(handlerErr, context.Canceled) || errors.Is(handlerErr, context.DeadlineExceeded) {
			return handlerErr
		}
		return w.client.FailJob(context.WithoutCancel(jobCtx), job.Key, FailJobRequest{
			Worker:       w.workerName,
			ErrorMessage: handlerErr.Error(),
		})
	}

	return w.client.CompleteJob(context.WithoutCancel(jobCtx), job.Key, CompleteJobRequest{
		Worker:    w.workerName,
		Variables: variables,
	})
}

func (w *Worker) startLockRenewer(ctx context.Context, cancel context.CancelFunc, job model.Job) func() error {
	ticker := time.NewTicker(w.lockRenewInterval)
	done := make(chan struct{})
	errCh := make(chan error, 1)

	go func() {
		defer close(done)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				err := w.client.ExtendJobLock(ctx, job.Key, ExtendJobLockRequest{
					Worker:         w.workerName,
					LockDurationMs: int(w.lockDuration / time.Millisecond),
				})
				if err != nil {
					if ctx.Err() != nil {
						// Renewer canceled during normal shutdown of this job.
						return
					}
					select {
					case errCh <- fmt.Errorf("failed to extend lock for job %d: %w", job.Key, err):
					default:
					}
					cancel()
					return
				}
			}
		}
	}()

	return func() error {
		cancel()
		<-done
		select {
		case err := <-errCh:
			return err
		default:
			return nil
		}
	}
}
