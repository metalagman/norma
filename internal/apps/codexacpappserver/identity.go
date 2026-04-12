package codexacp

import "strings"

type appServerIdentity struct {
	userAgent string
}

func parseAppServerIdentity(userAgent string) appServerIdentity {
	return appServerIdentity{userAgent: strings.TrimSpace(userAgent)}
}

func resolveAgentIdentity(requestedName string, identity appServerIdentity) (name string, version string) {
	name = strings.TrimSpace(requestedName)
	if name == "" {
		uaToken := strings.Fields(identity.userAgent)
		if len(uaToken) > 0 {
			parts := strings.SplitN(uaToken[0], "/", 2)
			if len(parts) == 2 {
				name = strings.TrimSpace(parts[0])
				version = strings.TrimSpace(parts[1])
			} else if len(parts) == 1 {
				name = strings.TrimSpace(parts[0])
			}
		}
	}
	if name == "" {
		name = DefaultAgentName
	}
	if strings.TrimSpace(version) == "" {
		version = DefaultAgentVersion
	}
	return name, version
}
