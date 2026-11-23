package mesh

// reconcileOnce runs the network + self-attach half of Gateway.reconcile.
func reconcileOnce(f *dockerFixture, agent string) error {
	mn, err := ensureNetwork(f.ctx, f.network, f.subnet, f.gw)
	if err != nil {
		return err
	}
	return ensureSelfConnected(f.ctx, mn, agent, f.gw, f.holders)
}

func ensureSelfConnectedForTest(f *dockerFixture, agent string) error {
	return ensureSelfConnected(f.ctx, f.meshNet(), agent, f.gw, f.holders)
}
