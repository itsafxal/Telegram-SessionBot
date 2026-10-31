package broadcast

// BroadcastMessage represents content to be broadcast to all registered users.
type BroadcastMessage struct {
	Text       string
	PhotoID    string
	VideoID    string
	DocumentID string
	Caption    string
}
