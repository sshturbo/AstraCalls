package main

import (
	cryptoRand "crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"go.mau.fi/whatsmeow"
	waBinary "go.mau.fi/whatsmeow/binary"
	"go.mau.fi/whatsmeow/proto/waCommon"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"google.golang.org/protobuf/proto"
)

type interactiveButton struct {
	Type        string `json:"type"`
	ID          string `json:"id"`
	Text        string `json:"text"`
	DisplayText string `json:"displayText"`
	URL         string `json:"url"`
	PhoneNumber string `json:"phoneNumber"`
	CopyCode    string `json:"copyCode"`
}

type interactiveButtonRequest struct {
	To          string              `json:"to"`
	Title       string              `json:"title"`
	Text        string              `json:"text"`
	Description string              `json:"description"`
	Footer      string              `json:"footer"`
	Buttons     []interactiveButton `json:"buttons"`
}

type listRowRequest struct {
	ID          string `json:"id"`
	RowID       string `json:"rowId"`
	Title       string `json:"title"`
	Description string `json:"description"`
}

type listSectionRequest struct {
	Title string           `json:"title"`
	Rows  []listRowRequest `json:"rows"`
}

type listMessageRequest struct {
	To          string               `json:"to"`
	Title       string               `json:"title"`
	Text        string               `json:"text"`
	Description string               `json:"description"`
	Footer      string               `json:"footer"`
	FooterText  string               `json:"footerText"`
	ButtonText  string               `json:"buttonText"`
	Sections    []listSectionRequest `json:"sections"`
}

func (s *server) sendMessageWithExtra(sess *Session, w http.ResponseWriter, r *http.Request, to string, msg *waE2E.Message, extra ...whatsmeow.SendRequestExtra) {
	jid, err := resolveRecipient(to)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	resp, err := sess.client.SendMessage(r.Context(), jid, msg, extra...)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"id": resp.ID, "to": jid.String(), "timestamp": resp.Timestamp.UnixMilli(),
	})
}

func interactiveBody(title, description, text string) string {
	body := strings.TrimSpace(description)
	if body == "" {
		body = strings.TrimSpace(text)
	}
	if strings.TrimSpace(title) == "" {
		return body
	}
	if body == "" {
		return strings.TrimSpace(title)
	}
	return "*" + strings.TrimSpace(title) + "*\n\n" + body
}

func interactiveNodes(flowName string, group bool) []waBinary.Node {
	nodes := []waBinary.Node{{
		Tag: "biz",
		Content: []waBinary.Node{{
			Tag:   "interactive",
			Attrs: waBinary.Attrs{"type": "native_flow", "v": "1"},
			Content: []waBinary.Node{{
				Tag:   "native_flow",
				Attrs: waBinary.Attrs{"name": flowName},
			}},
		}},
	}}
	if !group {
		nodes = append(nodes, waBinary.Node{Tag: "bot", Attrs: waBinary.Attrs{"biz_bot": "1"}})
	}
	return nodes
}

func (s *server) handleSendButton(w http.ResponseWriter, r *http.Request) {
	sess := s.pairedSession(w, r.PathValue("sid"))
	if sess == nil {
		return
	}
	var body interactiveButtonRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON body"})
		return
	}
	if strings.TrimSpace(body.To) == "" || len(body.Buttons) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "to and buttons required"})
		return
	}

	hasReply := false
	hasCTA := false
	for i := range body.Buttons {
		buttonType := strings.ToLower(strings.TrimSpace(body.Buttons[i].Type))
		if buttonType == "" {
			buttonType = "reply"
		}
		body.Buttons[i].Type = buttonType
		switch buttonType {
		case "reply":
			hasReply = true
		case "copy", "url", "call":
			hasCTA = true
		default:
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "button type must be reply, copy, url or call"})
			return
		}
	}
	if hasReply && hasCTA {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "reply buttons cannot be mixed with CTA buttons"})
		return
	}
	if hasReply && len(body.Buttons) > 3 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "maximum 3 reply buttons"})
		return
	}

	messageSecret := make([]byte, 32)
	_, _ = cryptoRand.Read(messageSecret)
	messageBody := interactiveBody(body.Title, body.Description, body.Text)
	group := strings.Contains(body.To, "@g.us")

	if hasReply {
		buttons := make([]*waE2E.ButtonsMessage_Button, 0, len(body.Buttons))
		for i, button := range body.Buttons {
			label := strings.TrimSpace(button.DisplayText)
			if label == "" {
				label = strings.TrimSpace(button.Text)
			}
			if label == "" {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "button text required"})
				return
			}
			id := strings.TrimSpace(button.ID)
			if id == "" {
				id = fmt.Sprintf("button_%d", i+1)
			}
			buttons = append(buttons, &waE2E.ButtonsMessage_Button{
				ButtonID: proto.String(id),
				ButtonText: &waE2E.ButtonsMessage_Button_ButtonText{
					DisplayText: proto.String(label),
				},
				Type: waE2E.ButtonsMessage_Button_RESPONSE.Enum(),
			})
		}
		buttonsMessage := &waE2E.ButtonsMessage{
			ContentText: proto.String(messageBody),
			FooterText:  proto.String(body.Footer),
			HeaderType:  waE2E.ButtonsMessage_EMPTY.Enum(),
			Buttons:     buttons,
		}
		msg := &waE2E.Message{
			DocumentWithCaptionMessage: &waE2E.FutureProofMessage{
				Message: &waE2E.Message{ButtonsMessage: buttonsMessage},
			},
			MessageContextInfo: &waE2E.MessageContextInfo{MessageSecret: messageSecret},
		}
		nodes := interactiveNodes("quick_reply", group)
		s.sendMessageWithExtra(sess, w, r, body.To, msg, whatsmeow.SendRequestExtra{AdditionalNodes: &nodes})
		return
	}

	buttons := make([]*waE2E.InteractiveMessage_NativeFlowMessage_NativeFlowButton, 0, len(body.Buttons))
	for i, button := range body.Buttons {
		label := strings.TrimSpace(button.DisplayText)
		if label == "" {
			label = strings.TrimSpace(button.Text)
		}
		if label == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "button text required"})
			return
		}
		var name string
		var params map[string]string
		switch button.Type {
		case "copy":
			name = "cta_copy"
			code := button.CopyCode
			if code == "" {
				code = button.ID
			}
			if code == "" {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "copyCode required for copy button"})
				return
			}
			params = map[string]string{"display_text": label, "id": fmt.Sprintf("copy_%d", i+1), "copy_code": code}
		case "url":
			name = "cta_url"
			if strings.TrimSpace(button.URL) == "" {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "url required for URL button"})
				return
			}
			params = map[string]string{"display_text": label, "url": button.URL, "merchant_url": button.URL}
		case "call":
			name = "cta_call"
			if strings.TrimSpace(button.PhoneNumber) == "" {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "phoneNumber required for call button"})
				return
			}
			params = map[string]string{"display_text": label, "phone_number": button.PhoneNumber}
		}
		paramsJSON, _ := json.Marshal(params)
		buttons = append(buttons, &waE2E.InteractiveMessage_NativeFlowMessage_NativeFlowButton{
			Name:             proto.String(name),
			ButtonParamsJSON: proto.String(string(paramsJSON)),
		})
	}
	messageParams := `{"from":"api","templateId":"` + strconv.FormatInt(time.Now().UnixMilli(), 10) + `"}`
	msg := &waE2E.Message{
		DocumentWithCaptionMessage: &waE2E.FutureProofMessage{
			Message: &waE2E.Message{
				InteractiveMessage: &waE2E.InteractiveMessage{
					Body:   &waE2E.InteractiveMessage_Body{Text: proto.String(messageBody)},
					Footer: &waE2E.InteractiveMessage_Footer{Text: proto.String(body.Footer)},
					InteractiveMessage: &waE2E.InteractiveMessage_NativeFlowMessage_{
						NativeFlowMessage: &waE2E.InteractiveMessage_NativeFlowMessage{
							Buttons:           buttons,
							MessageParamsJSON: proto.String(messageParams),
							MessageVersion:    proto.Int32(1),
						},
					},
				},
			},
		},
		MessageContextInfo: &waE2E.MessageContextInfo{MessageSecret: messageSecret},
	}
	nodes := interactiveNodes("mixed", group)
	s.sendMessageWithExtra(sess, w, r, body.To, msg, whatsmeow.SendRequestExtra{AdditionalNodes: &nodes})
}

func (s *server) handleSendList(w http.ResponseWriter, r *http.Request) {
	sess := s.pairedSession(w, r.PathValue("sid"))
	if sess == nil {
		return
	}
	var body listMessageRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON body"})
		return
	}
	if strings.TrimSpace(body.To) == "" || len(body.Sections) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "to and sections required"})
		return
	}
	sections := make([]*waE2E.ListMessage_Section, 0, len(body.Sections))
	rowCount := 0
	for sectionIndex, section := range body.Sections {
		rows := make([]*waE2E.ListMessage_Row, 0, len(section.Rows))
		for rowIndex, row := range section.Rows {
			if strings.TrimSpace(row.Title) == "" {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "row title required"})
				return
			}
			id := strings.TrimSpace(row.RowID)
			if id == "" {
				id = strings.TrimSpace(row.ID)
			}
			if id == "" {
				id = fmt.Sprintf("row_%d_%d", sectionIndex+1, rowIndex+1)
			}
			rows = append(rows, &waE2E.ListMessage_Row{
				Title:       proto.String(row.Title),
				Description: proto.String(row.Description),
				RowID:       proto.String(id),
			})
			rowCount++
		}
		if len(rows) > 0 {
			sectionTitle := section.Title
			if strings.TrimSpace(sectionTitle) == "" {
				sectionTitle = " "
			}
			sections = append(sections, &waE2E.ListMessage_Section{Title: proto.String(sectionTitle), Rows: rows})
		}
	}
	if rowCount == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "at least one row required"})
		return
	}
	buttonText := strings.TrimSpace(body.ButtonText)
	if buttonText == "" {
		buttonText = "Abrir menu"
	}
	footer := body.FooterText
	if footer == "" {
		footer = body.Footer
	}
	listType := waE2E.ListMessage_SINGLE_SELECT
	listMessage := &waE2E.ListMessage{
		Title:       proto.String(body.Title),
		Description: proto.String(interactiveBody("", body.Description, body.Text)),
		ButtonText:  proto.String(buttonText),
		FooterText:  proto.String(footer),
		ListType:    &listType,
		Sections:    sections,
	}
	messageSecret := make([]byte, 32)
	_, _ = cryptoRand.Read(messageSecret)
	msg := &waE2E.Message{
		DocumentWithCaptionMessage: &waE2E.FutureProofMessage{
			Message: &waE2E.Message{ListMessage: listMessage},
		},
		MessageContextInfo: &waE2E.MessageContextInfo{MessageSecret: messageSecret},
	}
	nodes := []waBinary.Node{{
		Tag: "biz",
		Content: []waBinary.Node{{
			Tag:   "list",
			Attrs: waBinary.Attrs{"v": "2", "type": "single_select"},
		}},
	}}
	if !strings.Contains(body.To, "@g.us") {
		nodes = append(nodes, waBinary.Node{Tag: "bot", Attrs: waBinary.Attrs{"biz_bot": "1"}})
	}
	s.sendMessageWithExtra(sess, w, r, body.To, msg, whatsmeow.SendRequestExtra{AdditionalNodes: &nodes})
}

func (s *server) handleSendLocation(w http.ResponseWriter, r *http.Request) {
	sess := s.pairedSession(w, r.PathValue("sid"))
	if sess == nil {
		return
	}
	var body struct {
		To        string  `json:"to"`
		Latitude  float64 `json:"latitude"`
		Longitude float64 `json:"longitude"`
		Name      string  `json:"name"`
		Address   string  `json:"address"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || strings.TrimSpace(body.To) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "to, latitude and longitude required"})
		return
	}
	if body.Latitude < -90 || body.Latitude > 90 || body.Longitude < -180 || body.Longitude > 180 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid latitude or longitude"})
		return
	}
	msg := &waE2E.Message{LocationMessage: &waE2E.LocationMessage{
		DegreesLatitude:  proto.Float64(body.Latitude),
		DegreesLongitude: proto.Float64(body.Longitude),
		Name:             proto.String(body.Name),
		Address:          proto.String(body.Address),
	}}
	s.sendMessageWithExtra(sess, w, r, body.To, msg)
}

func vCardEscape(value string) string {
	replacer := strings.NewReplacer("\\", "\\\\", "\n", "\\n", ";", "\\;", ",", "\\,")
	return replacer.Replace(strings.TrimSpace(value))
}

func (s *server) handleSendContact(w http.ResponseWriter, r *http.Request) {
	sess := s.pairedSession(w, r.PathValue("sid"))
	if sess == nil {
		return
	}
	var body struct {
		To      string `json:"to"`
		Contact struct {
			FullName     string `json:"fullName"`
			Phone        string `json:"phone"`
			Organization string `json:"organization"`
		} `json:"contact"`
		FullName     string `json:"fullName"`
		Phone        string `json:"phone"`
		Organization string `json:"organization"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON body"})
		return
	}
	fullName := body.Contact.FullName
	phone := body.Contact.Phone
	organization := body.Contact.Organization
	if fullName == "" {
		fullName = body.FullName
	}
	if phone == "" {
		phone = body.Phone
	}
	if organization == "" {
		organization = body.Organization
	}
	if strings.TrimSpace(body.To) == "" || strings.TrimSpace(fullName) == "" || normalizePhone(phone) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "to, contact.fullName and contact.phone required"})
		return
	}
	phone = normalizePhone(phone)
	vcard := "BEGIN:VCARD\r\nVERSION:3.0\r\nFN:" + vCardEscape(fullName) + "\r\n"
	if strings.TrimSpace(organization) != "" {
		vcard += "ORG:" + vCardEscape(organization) + "\r\n"
	}
	vcard += "TEL;TYPE=CELL;TYPE=VOICE;waid=" + phone + ":+" + phone + "\r\nEND:VCARD"
	msg := &waE2E.Message{ContactMessage: &waE2E.ContactMessage{
		DisplayName: proto.String(fullName),
		Vcard:       proto.String(vcard),
	}}
	s.sendMessageWithExtra(sess, w, r, body.To, msg)
}

func (s *server) handleSendPoll(w http.ResponseWriter, r *http.Request) {
	sess := s.pairedSession(w, r.PathValue("sid"))
	if sess == nil {
		return
	}
	var body struct {
		To              string   `json:"to"`
		Question        string   `json:"question"`
		Options         []string `json:"options"`
		MaxAnswer       int      `json:"maxAnswer"`
		SelectableCount int      `json:"selectableCount"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON body"})
		return
	}
	if strings.TrimSpace(body.To) == "" || strings.TrimSpace(body.Question) == "" || len(body.Options) < 2 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "to, question and at least two options required"})
		return
	}
	for _, option := range body.Options {
		if strings.TrimSpace(option) == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "poll options cannot be empty"})
			return
		}
	}
	maxAnswer := body.MaxAnswer
	if maxAnswer == 0 {
		maxAnswer = body.SelectableCount
	}
	if maxAnswer <= 0 {
		maxAnswer = 1
	}
	if maxAnswer > len(body.Options) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "maxAnswer cannot exceed number of options"})
		return
	}
	msg := sess.client.BuildPollCreation(body.Question, body.Options, maxAnswer)
	s.sendMessageWithExtra(sess, w, r, body.To, msg)
}

func (s *server) handleSendReaction(w http.ResponseWriter, r *http.Request) {
	sess := s.pairedSession(w, r.PathValue("sid"))
	if sess == nil {
		return
	}
	var body struct {
		Chat        string `json:"chat"`
		To          string `json:"to"`
		MessageID   string `json:"messageId"`
		ID          string `json:"id"`
		Emoji       string `json:"emoji"`
		Reaction    string `json:"reaction"`
		FromMe      bool   `json:"fromMe"`
		Participant string `json:"participant"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON body"})
		return
	}
	chat := body.Chat
	if chat == "" {
		chat = body.To
	}
	messageID := body.MessageID
	if messageID == "" {
		messageID = body.ID
	}
	reaction := body.Emoji
	if reaction == "" {
		reaction = body.Reaction
	}
	if reaction == "remove" {
		reaction = ""
	}
	if strings.TrimSpace(chat) == "" || strings.TrimSpace(messageID) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "chat and messageId required"})
		return
	}
	jid, err := resolveRecipient(chat)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	key := &waCommon.MessageKey{
		RemoteJID: proto.String(jid.String()),
		FromMe:    proto.Bool(body.FromMe),
		ID:        proto.String(messageID),
	}
	if strings.TrimSpace(body.Participant) != "" {
		participant, participantErr := resolveRecipient(body.Participant)
		if participantErr != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid participant"})
			return
		}
		key.Participant = proto.String(participant.String())
	}
	msg := &waE2E.Message{ReactionMessage: &waE2E.ReactionMessage{
		Key:               key,
		Text:              proto.String(reaction),
		SenderTimestampMS: proto.Int64(time.Now().UnixMilli()),
	}}
	s.sendMessageWithExtra(sess, w, r, chat, msg)
}

func (s *server) handleSendSticker(w http.ResponseWriter, r *http.Request) {
	sess := s.pairedSession(w, r.PathValue("sid"))
	if sess == nil {
		return
	}
	var body struct {
		To       string `json:"to"`
		Base64   string `json:"base64"`
		URL      string `json:"url"`
		Sticker  string `json:"sticker"`
		Mimetype string `json:"mimetype"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || strings.TrimSpace(body.To) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "to and sticker URL/base64 required"})
		return
	}
	if body.URL == "" && strings.HasPrefix(body.Sticker, "http") {
		body.URL = body.Sticker
	}
	if body.Base64 == "" && body.URL == "" && body.Sticker != "" {
		body.Base64 = body.Sticker
	}
	data, err := fetchMedia(body.Base64, body.URL)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if len(data) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "empty sticker"})
		return
	}
	mime := strings.TrimSpace(body.Mimetype)
	if mime == "" {
		mime = http.DetectContentType(data)
	}
	if mime != "image/webp" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "sticker must be WebP (image/webp)"})
		return
	}
	uploaded, err := sess.client.Upload(r.Context(), data, whatsmeow.MediaImage)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	msg := &waE2E.Message{StickerMessage: &waE2E.StickerMessage{
		URL:           proto.String(uploaded.URL),
		DirectPath:    proto.String(uploaded.DirectPath),
		MediaKey:      uploaded.MediaKey,
		Mimetype:      proto.String(mime),
		FileEncSHA256: uploaded.FileEncSHA256,
		FileSHA256:    uploaded.FileSHA256,
		FileLength:    proto.Uint64(uint64(len(data))),
	}}
	s.sendMessageWithExtra(sess, w, r, body.To, msg)
}

var errUnsupportedInteractive = errors.New("unsupported interactive message")

// Keep a concrete sentinel available for integrations that want to classify
// unsupported interactive payloads without string matching.
func unsupportedInteractiveError() error { return errUnsupportedInteractive }
