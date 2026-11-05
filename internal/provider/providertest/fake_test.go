package providertest

import "testing"

// The fake has to honour the same contract, or tests built on it prove nothing.
func TestFakeConforms(t *testing.T) {
	Run(t, NewFake(ListPageSize))
}
