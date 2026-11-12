// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package onboarding

import "sync"

// parallelReads bounds independent reads without canceling sibling evidence on failure.
// Each task owns its output; callers assemble reports in their original order after the join.
func parallelReads(tasks ...func()) {
	var wg sync.WaitGroup
	work := make(chan func(), len(tasks))
	for _, task := range tasks {
		work <- task
	}
	close(work)
	for range min(4, len(tasks)) {
		wg.Go(func() {
			for task := range work {
				task()
			}
		})
	}
	wg.Wait()
}
