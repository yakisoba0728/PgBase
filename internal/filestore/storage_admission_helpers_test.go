package filestore

// active reports the requests subject has admitted.
func (s *storageSubjectSlots) active(subject string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	if entry := s.entries[subject]; entry != nil {
		return len(entry.slots)
	}
	return 0
}
