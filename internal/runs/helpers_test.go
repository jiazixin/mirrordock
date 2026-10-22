package runs

// Get and ClearLogs bind the engine context for tests that do not exercise
// request cancellation.

func (e *Engine) Get(id int64) (*Run, error) {
	return e.GetContext(e.ctx, id)
}

func (e *Engine) ClearLogs() (int, error) {
	return e.ClearLogsContext(e.ctx)
}
