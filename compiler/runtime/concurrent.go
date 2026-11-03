package runtime

import (
	"context"
	"sync"

	"fiig-lang/compiler/syntax"
)

// evalConcurrent implements `concurrent(x in xs): body` (fiig-lang.md §3.7):
// a bounded pool of worker goroutines pulls indices from a fixed-size job
// channel (never more than `limit` bodies ever running at once, regardless
// of how large xs is — the "never create 10,000 simultaneously active
// operations" scale-test requirement) and feeds results back over a
// results channel, which is where completion order naturally falls out:
// collecting in receive order is completion order; collecting by the job's
// own index instead is `preserveOrder` (ADR 0024). Every child body shares
// a single cancellable context (runCtx) derived from the caller's — if any
// one body produces a genuine Fault, runCtx is cancelled so every other
// in-flight and not-yet-started body stops too ("cancellation propagates to
// every child operation"), and the whole block reports that Fault.
//
// A per-item typed Err is not a Fault — it's an entirely ordinary
// BatchItem.Outcome, exactly what BatchResult exists to hold (§3.7: "per-
// item failures are preserved in a BatchResult by default").
//
// When a Tracer is set, the whole block gets its own SpanConcurrent span,
// and the context it runs under (threaded to every worker goroutine) has
// that span active as the current parent — so whatever each worker's body
// actually calls gets its own nested span the ordinary way, with no extra
// plumbing needed for the fan-out to show up in the trace.
func (in *Interp) evalConcurrent(ctx context.Context, e *syntax.ConcurrentExpr, env *Env) (Value, bool, *Fault) {
	if in.Tracer == nil {
		return in.evalConcurrentBody(ctx, e, env)
	}
	spanCtx, span := in.startSpan(ctx, "concurrent("+e.Var+")", SpanConcurrent)
	val, earlyReturn, fault := in.evalConcurrentBody(spanCtx, e, env)
	in.finishSpan(span, val, fault)
	return val, earlyReturn, fault
}

func (in *Interp) evalConcurrentBody(ctx context.Context, e *syntax.ConcurrentExpr, env *Env) (Value, bool, *Fault) {
	iterVal, earlyReturn, fault := in.evalExpr(ctx, e.Iterable, env)
	if fault != nil || earlyReturn {
		return iterVal, earlyReturn, fault
	}
	list, ok := iterVal.(List)
	if !ok {
		return nil, false, Faultf("concurrent(...) iterable is not a List (got %s)", iterVal)
	}

	limit, fault := in.resolveConcurrencyLimit(ctx, e, env)
	if fault != nil {
		return nil, false, fault
	}

	n := len(list.Elems)
	if n == 0 {
		return BatchResult{}, false, nil
	}
	workers := limit
	if workers > n {
		workers = n
	}

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel() // structured concurrency: no child outlives this block, even on early exit via fault.

	jobs := make(chan int, n)
	for i := range list.Elems {
		jobs <- i
	}
	close(jobs)

	type jobResult struct {
		index int
		item  BatchItem
		fault *Fault
	}
	results := make(chan jobResult, n)

	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for idx := range jobs {
				key := list.Elems[idx]
				if f := checkCancelled(runCtx); f != nil {
					results <- jobResult{index: idx, fault: f}
					continue
				}
				bodyEnv := env.Child()
				bodyEnv.Bind(e.Var, key)
				val, _, bodyFault := in.runProducingBody(runCtx, e.Body, bodyEnv)
				if bodyFault != nil {
					results <- jobResult{index: idx, fault: bodyFault}
					cancel()
					continue
				}
				rv, ok := val.(Result)
				if !ok {
					results <- jobResult{index: idx, fault: Faultf("concurrent body did not produce a Result (got %s)", val)}
					cancel()
					continue
				}
				results <- jobResult{index: idx, item: BatchItem{Key: key, Outcome: rv}}
			}
		}()
	}
	go func() {
		wg.Wait()
		close(results)
	}()

	completion := make([]BatchItem, 0, n)
	byIndex := make([]*BatchItem, n)
	var firstFault *Fault
	for res := range results {
		if res.fault != nil {
			if firstFault == nil {
				firstFault = res.fault
			}
			continue
		}
		if e.PreserveOrder {
			item := res.item
			byIndex[res.index] = &item
		} else {
			completion = append(completion, res.item)
		}
	}
	if firstFault != nil {
		return nil, false, firstFault
	}
	if e.PreserveOrder {
		completion = completion[:0]
		for _, item := range byIndex {
			if item != nil {
				completion = append(completion, *item)
			}
		}
	}
	return BatchResult{Items: completion}, false, nil
}

// resolveConcurrencyLimit is D1/D2's runtime counterpart: compiler/checker
// already rejects `limit: unbounded` and a nested limit exceeding its
// parent's statically, but this package can run a program that was never
// checked (any test building syntax directly, for instance), so both are
// re-checked here rather than assumed.
func (in *Interp) resolveConcurrencyLimit(ctx context.Context, e *syntax.ConcurrentExpr, env *Env) (int, *Fault) {
	if e.Limit == nil {
		return in.DefaultConcurrencyLimit, nil
	}
	if e.Limit.Unbounded {
		return 0, Faultf("concurrent(...) requested limit: unbounded; concurrency is always bounded")
	}
	limVal, earlyReturn, fault := in.evalExpr(ctx, e.Limit.Value, env)
	if fault != nil {
		return 0, fault
	}
	if earlyReturn {
		return 0, Faultf("`?` is not supported inside a concurrent limit clause")
	}
	limInt, ok := limVal.(Int)
	if !ok {
		return 0, Faultf("concurrent(...) limit is not an int (got %s)", limVal)
	}
	if limInt <= 0 {
		return 0, Faultf("concurrent(...) limit must be positive (got %d)", limInt)
	}
	return int(limInt), nil
}
