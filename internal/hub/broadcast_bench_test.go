package hub

import (
	"fmt"
	"testing"
)

// benchmarkBroadcast measures one full broadcast to a room of n participants,
// every one of whom has voted — the worst case for the per-recipient masking
// loop, since every vote has to be rewritten for every other viewer.
func benchmarkBroadcast(b *testing.B, participants int) {
	fs := &fakeStore{}
	var s Store = fs

	r := newRoom(&s, "BENCH", defaultDeck)
	r.story = "PP-1421 Rework the invoice reconciliation screen"
	r.facilitatorID = "c0"

	stop := make(chan struct{})
	b.Cleanup(func() { close(stop) })

	for i := 0; i < participants; i++ {
		c := newTestClient(fmt.Sprintf("c%d", i), fmt.Sprintf("Person %d", i))
		c.participant.Vote = "5"
		r.participants[c.id] = c

		// Drain, or the buffers fill and the room starts dropping clients.
		go func(c *Client) {
			for {
				select {
				case <-c.send:
				case <-stop:
					return
				}
			}
		}(c)
	}

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		r.broadcastStateToAll()
	}
}

func BenchmarkBroadcastStateToAll5(b *testing.B)  { benchmarkBroadcast(b, 5) }
func BenchmarkBroadcastStateToAll10(b *testing.B) { benchmarkBroadcast(b, 10) }
func BenchmarkBroadcastStateToAll20(b *testing.B) { benchmarkBroadcast(b, 20) }
func BenchmarkBroadcastStateToAll50(b *testing.B) { benchmarkBroadcast(b, 50) }
