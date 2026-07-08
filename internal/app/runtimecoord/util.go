package runtimecoord

func normalizeAgentKind(kind AgentKind) AgentKind {
	switch kind {
	case "":
		return ""
	case AgentClaude:
		return AgentClaude
	case AgentCodex:
		return AgentCodex
	default:
		return ""
	}
}

func normalizeTenant(tenant TenantBrand) TenantBrand {
	switch tenant {
	case TenantLark:
		return TenantLark
	default:
		return TenantFeishu
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
