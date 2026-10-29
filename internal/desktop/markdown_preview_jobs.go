package desktop

import (
	"context"

	"silverwing-go/internal/asyncjob"
)

// Shared media jobs outlive individual build elements but never the desktop's
// media owner. Document cancellation remains independent from feature shutdown.
func (s *app) beginMarkdownPreviewJob() (*asyncjob.Lease, error) {
	if s.materialsUnit.state.markdownPreviewJobs == nil {
		s.materialsUnit.state.markdownPreviewJobs = &asyncjob.Group{}
	}
	return s.materialsUnit.state.markdownPreviewJobs.Begin(context.Background(), asyncjob.Options{CancelOnClose: true})
}

func (s *app) newMarkdownImagePool() *nativeImagePool {
	pool := newNativeImagePool()
	lease, err := s.beginMarkdownPreviewJob()
	if err != nil {
		pool.close()
		return pool
	}
	go func() {
		defer lease.Cancel()
		defer lease.Complete(nil)
		select {
		case <-pool.done:
			return
		case <-lease.Context().Done():
			pool.close()
			<-pool.done
		}
	}()
	return pool
}

func (s *materialsController) stopMarkdownPreviewJobs() {
	if s.state.markdownPreviewJobs == nil {
		s.state.markdownPreviewJobs = &asyncjob.Group{}
	}
	s.state.markdownPreviewJobs.Close()
}

func (s *materialsController) closeMarkdownPreviewJobs(ctx context.Context) error {
	s.stopMarkdownPreviewJobs()
	return s.state.markdownPreviewJobs.Wait(ctx)
}

func (s *app) markdownPreviewJobsClosed() bool {
	return s.materialsUnit.state.markdownPreviewJobs != nil && s.materialsUnit.state.markdownPreviewJobs.Closed()
}
