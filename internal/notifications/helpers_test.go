package notifications

// Records returns the newest page of delivery records.
func (s *Service) Records() ([]Record, error) {
	records, _, err := s.RecordPage(s.ctx, 30, 0)
	return records, err
}
