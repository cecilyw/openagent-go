package components

// Loading is the spinner used by the status bar while a turn is in flight.
type Loading struct {
	Frames []string
	frame  int
}

func NewLoading(frames []string) Loading {
	if len(frames) == 0 {
		frames = []string{"|", "/", "-", "\\"}
	}
	return Loading{
		Frames: frames,
		frame:  0,
	}
}

func (l Loading) Tick() Loading {
	if len(l.Frames) > 0 {
		l.frame = (l.frame + 1) % len(l.Frames)
	}
	return l
}

func (l Loading) View() string {
	if len(l.Frames) == 0 || l.frame >= len(l.Frames) {
		return ""
	}
	return l.Frames[l.frame]
}
