package main

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

type fakeWorkerRunner struct {
	runCalls     int
	processCalls int
	processed    bool
	err          error
}

func (f *fakeWorkerRunner) Run(context.Context) error {
	f.runCalls++
	return f.err
}

func (f *fakeWorkerRunner) ProcessNext(context.Context) (bool, error) {
	f.processCalls++
	return f.processed, f.err
}

func TestRunWorkerOnceProcessesOneTaskAndExits(t *testing.T) {
	worker := &fakeWorkerRunner{processed: true}

	err := runWorker(context.Background(), worker, true)

	require.NoError(t, err)
	require.Equal(t, 1, worker.processCalls)
	require.Zero(t, worker.runCalls)
}

func TestRunWorkerDefaultRunsPollingLoop(t *testing.T) {
	worker := &fakeWorkerRunner{}

	err := runWorker(context.Background(), worker, false)

	require.NoError(t, err)
	require.Equal(t, 1, worker.runCalls)
	require.Zero(t, worker.processCalls)
}
