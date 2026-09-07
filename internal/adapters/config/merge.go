package config

func merge(base, overlay map[string]any) map[string]any {
	result := cloneMap(base)
	for key, value := range overlay {
		baseMap, baseOK := result[key].(map[string]any)
		overlayMap, overlayOK := value.(map[string]any)
		if baseOK && overlayOK {
			result[key] = merge(baseMap, overlayMap)
			continue
		}
		result[key] = clone(value)
	}
	return result
}

func cloneMap(value map[string]any) map[string]any {
	result := make(map[string]any, len(value))
	for key, item := range value {
		result[key] = clone(item)
	}
	return result
}

func clone(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		return cloneMap(typed)
	case []any:
		result := make([]any, len(typed))
		for index, item := range typed {
			result[index] = clone(item)
		}
		return result
	default:
		return typed
	}
}
