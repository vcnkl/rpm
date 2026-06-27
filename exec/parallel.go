package exec

import (
	"context"
	"sync"

	"github.com/vcnkl/rpm/dag"
)

type ParallelExecutor struct {
	maxWorkers int
}

func NewParallelExecutor(maxWorkers int) *ParallelExecutor {
	if maxWorkers <= 0 {
		maxWorkers = 1
	}
	return &ParallelExecutor{maxWorkers: maxWorkers}
}

type TaskFunc func(ctx context.Context, node *dag.Node) error

func (p *ParallelExecutor) Execute(ctx context.Context, nodes []*dag.Node, fn TaskFunc) map[string]error {
	results := make(map[string]error)
	var mu sync.Mutex
	setResult := func(id string, err error) {
		mu.Lock()
		results[id] = err
		mu.Unlock()
	}
	getResult := func(id string) error {
		mu.Lock()
		defer mu.Unlock()
		return results[id]
	}

	done := make(map[string]chan struct{}, len(nodes))
	for _, node := range nodes {
		done[node.ID] = make(chan struct{})
	}

	sem := make(chan struct{}, p.maxWorkers)
	var wg sync.WaitGroup

	for _, node := range nodes {
		wg.Add(1)
		go func(n *dag.Node) {
			defer wg.Done()
			defer close(done[n.ID])

			anyDepFailed := false
			for _, dep := range n.Deps {
				depDone, tracked := done[dep.ID]
				if !tracked {
					continue
				}
				select {
				case <-ctx.Done():
					setResult(n.ID, ctx.Err())
					return
				case <-depDone:
				}
				if getResult(dep.ID) != nil {
					anyDepFailed = true
				}
			}

			if anyDepFailed {
				setResult(n.ID, &DependencyFailedError{TargetID: n.ID})
				return
			}

			select {
			case <-ctx.Done():
				setResult(n.ID, ctx.Err())
				return
			case sem <- struct{}{}:
			}
			err := fn(ctx, n)
			<-sem
			setResult(n.ID, err)
		}(node)
	}

	wg.Wait()

	return results
}

type DependencyFailedError struct {
	TargetID string
}

func (e *DependencyFailedError) Error() string {
	return "dependency failed for target: " + e.TargetID
}
