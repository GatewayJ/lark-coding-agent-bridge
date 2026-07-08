package cardkit

import "github.com/zarazhangrui/lark-coding-agent-bridge/internal/app/cardrender"

type JSONCard map[string]any

func RenderRunCardKit(state cardrender.RunState, options cardrender.RenderOptions) JSONCard {
	return RenderCardView(cardrender.RenderCard(state, options))
}

func RenderCardView(view cardrender.CardView) JSONCard {
	elements := make([]any, 0, len(view.Elements)+len(view.Actions))
	for _, element := range view.Elements {
		elements = append(elements, renderElement(element))
	}
	for _, action := range view.Actions {
		elements = append(elements, renderAction(action))
	}
	return JSONCard{
		"schema": "2.0",
		"config": map[string]any{
			"streaming_mode": view.Streaming,
			"summary": map[string]any{
				"content": view.Summary,
			},
		},
		"body": map[string]any{
			"elements": elements,
		},
	}
}

func renderElement(element cardrender.CardElement) any {
	switch element.Kind {
	case cardrender.ElementPanel:
		if element.Panel == nil {
			return markdown("")
		}
		return collapsiblePanel(*element.Panel)
	case cardrender.ElementNote:
		return map[string]any{
			"tag":       "markdown",
			"content":   element.Text,
			"text_size": "notation",
		}
	default:
		return markdown(element.Text)
	}
}

func collapsiblePanel(panel cardrender.PanelView) map[string]any {
	border := panel.Border
	if border == "" {
		border = "grey"
	}
	return map[string]any{
		"tag":      "collapsible_panel",
		"expanded": panel.Expanded,
		"header":   panelHeader(panel.Title),
		"border": map[string]any{
			"color":         border,
			"corner_radius": "5px",
		},
		"vertical_spacing": "8px",
		"padding":          "8px 8px 8px 8px",
		"elements": []any{
			map[string]any{
				"tag":       "markdown",
				"content":   panel.Body,
				"text_size": "notation",
			},
		},
	}
}

func panelHeader(title string) map[string]any {
	return map[string]any{
		"title": map[string]any{
			"tag":     "markdown",
			"content": title,
		},
		"vertical_align": "center",
		"icon": map[string]any{
			"tag":   "standard_icon",
			"token": "down-small-ccm_outlined",
			"size":  "16px 16px",
		},
		"icon_position":       "follow_text",
		"icon_expanded_angle": -180,
	}
}

func renderAction(action cardrender.Action) map[string]any {
	style := string(action.Style)
	if style == "" {
		style = "default"
	}
	value := action.Value
	if value == nil {
		value = map[string]any{}
		if action.ID != "" {
			value["cmd"] = action.ID
		}
	}
	return map[string]any{
		"tag": "button",
		"text": map[string]any{
			"tag":     "plain_text",
			"content": action.Text,
		},
		"type": style,
		"behaviors": []any{
			map[string]any{
				"type":  "callback",
				"value": value,
			},
		},
	}
}

func markdown(content string) map[string]any {
	return map[string]any{"tag": "markdown", "content": content}
}
