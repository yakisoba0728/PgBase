package selfupdate

// Selection controls signed release eligibility. Source builds must name one
// exact version, preventing an unreviewed development binary from following a
// moving latest pointer.
type Selection struct {
	Current        Build
	Channel        string
	Requested      string
	AllowDowngrade bool
}

func Select(manifest Manifest, selection Selection) (Candidate, error) {
	channel := selection.Channel
	if channel == "" {
		channel = "stable"
	}
	if channel != manifest.Channel || !validChannel(channel) {
		return Candidate{}, ErrInvalidInput
	}
	current, currentReleased := parseVersion(selection.Current.Version)
	if !currentReleased && selection.Requested == "" {
		return Candidate{}, ErrInvalidInput
	}
	var chosen *Release
	for index := range manifest.Releases {
		release := &manifest.Releases[index]
		parsed, _ := parseVersion(release.Version)
		if channel == "stable" && len(parsed.pre) != 0 {
			continue
		}
		if selection.Requested != "" {
			if release.Version == selection.Requested {
				chosen = release
			}
			continue
		}
		if !currentReleased || compareVersion(parsed, current) <= 0 {
			continue
		}
		if chosen == nil {
			chosen = release
			continue
		}
		other, _ := parseVersion(chosen.Version)
		if compareVersion(parsed, other) > 0 {
			chosen = release
		}
	}
	if chosen == nil {
		return Candidate{}, ErrNoUpdate
	}
	target, _ := parseVersion(chosen.Version)
	if currentReleased {
		comparison := compareVersion(target, current)
		if comparison == 0 {
			return Candidate{}, ErrNoUpdate
		}
		if comparison < 0 && !selection.AllowDowngrade {
			return Candidate{}, ErrDowngradeRefused
		}
	}
	minimum, _ := parseVersion(chosen.MinUpdaterVersion)
	if currentReleased && compareVersion(current, minimum) < 0 {
		return Candidate{}, ErrInvalidInput
	}
	for _, artifact := range chosen.Artifacts {
		if artifact.GOOS == selection.Current.GOOS && artifact.GOARCH == selection.Current.GOARCH {
			return Candidate{Release: *chosen, Artifact: artifact}, nil
		}
	}
	return Candidate{}, ErrUnsupportedPlatform
}
