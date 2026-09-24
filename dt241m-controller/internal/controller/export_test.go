package controller

// DiscoveryRunCount is the number of full scans started so far.
func (c *Controller) DiscoveryRunCount() int { return int(c.discoveryRuns.Load()) }

// DiscoveryRunning reports whether a full scan is in progress.
func (c *Controller) DiscoveryRunning() bool {
	c.discoveryMu.Lock()
	defer c.discoveryMu.Unlock()
	return c.discoveryDone != nil
}
