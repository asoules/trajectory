package main

// Conservative retained-data accounting, not a measurement of Go heap/RSS.
// Shared strings are deliberately charged at each reference. Temporary JSON
// decoding, materialized views, and response encoding also use process memory.
func retainedSize(v any) int64 {
	switch x := v.(type) {
	case string:
		return 16 + int64(len(x))
	case M:
		n := int64(64)
		for k, value := range x {
			n += 96 + int64(len(k)) + retainedSize(value)
		}
		return n
	case []any:
		n := int64(24 + cap(x)*16)
		for _, value := range x {
			n += retainedSize(value)
		}
		return n
	case []M:
		n := int64(24 + cap(x)*8)
		for _, value := range x {
			n += retainedSize(value)
		}
		return n
	case nil:
		return 0
	default:
		return 16
	}
}

func (s *parseState) put(dst map[string]M, key string, value M) {
	if old, ok := dst[key]; ok {
		s.bytes -= retainedSize(old)
	} else {
		s.bytes += 96 + int64(len(key))
	}
	s.bytes += retainedSize(value)
	dst[key] = value
}

func (s *parseState) scalarBytes() int64 {
	return retainedSize(s.Meta) + retainedSize(s.Tokens) + int64(len(s.Current)+len(s.Title))
}

func (s *parseState) label(turn, text string) {
	if old, ok := s.TurnLabels[turn]; ok {
		s.bytes -= int64(len(old))
	} else {
		s.bytes += 96 + int64(len(turn))
	}
	s.bytes += int64(len(text))
	s.TurnLabels[turn] = text
}
