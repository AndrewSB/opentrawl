package messages

import (
	"encoding/base64"
	"errors"
	"fmt"
	"math"
	"net/url"
	"strings"
	"unicode/utf8"

	"google.golang.org/protobuf/encoding/protowire"
	"howett.net/plist"
)

const appleCashBalloonBundleIDSuffix = "com.apple.PassbookUIService.PeerPaymentMessagesExtension"

type AppleCashMessageKind int32

const (
	AppleCashMessageKindUnknown          AppleCashMessageKind = 0
	AppleCashMessageKindPayment          AppleCashMessageKind = 1
	AppleCashMessageKindRequest          AppleCashMessageKind = 2
	AppleCashMessageKindRecurringPayment AppleCashMessageKind = 3
)

type AppleCashMessagesContext int32

const (
	AppleCashMessagesContextUnknown    AppleCashMessagesContext = 0
	AppleCashMessagesContextIndividual AppleCashMessagesContext = 1
	AppleCashMessagesContextGroup      AppleCashMessagesContext = 2
)

type AppleCashPaymentSource int32

type AppleCashDecimalAmount struct {
	Version  uint32
	Exponent int32
	Length   int32
	Negative bool
	Compact  bool
	Reserved int32
	Mantissa []byte
}

type AppleCashMessage struct {
	Version                      uint32
	Identifier                   string
	Kind                         AppleCashMessageKind
	CurrencyCode                 string
	LegacyAmount                 int64
	SenderAddress                string
	RecipientAddress             string
	RequestToken                 string
	PaymentIdentifier            string
	TransactionIdentifier        string
	Memo                         string
	RequestDeviceScoreIdentifier string
	PaymentSource                AppleCashPaymentSource
	RecurringPaymentIdentifier   string
	RecurringPaymentEmoji        string
	RecurringPaymentColor        string
	RecurringPaymentStartDate    float64
	RecurringPaymentFrequency    string
	DecimalAmount                *AppleCashDecimalAmount
	LocalData                    []byte
	MessagesContext              AppleCashMessagesContext
	PaymentSignature             string
	MessagesGroupIdentifier      string
	SourceDisplayText            string
}

func decodeAppleCashMessage(balloonBundleID string, payloadData []byte) (*AppleCashMessage, error) {
	balloonBundleID = strings.TrimSpace(balloonBundleID)
	if balloonBundleID != appleCashBalloonBundleIDSuffix &&
		!strings.HasSuffix(balloonBundleID, ":"+appleCashBalloonBundleIDSuffix) {
		return nil, nil
	}
	var archive any
	if _, err := plist.Unmarshal(payloadData, &archive); err != nil {
		return nil, fmt.Errorf("decode Apple Cash binary property list: %w", err)
	}
	root, err := unarchiveAppleCashPropertyList(archive)
	if err != nil {
		return nil, err
	}
	paymentDataURL, _ := root["URL"].(string)
	paymentData, err := decodeAppleCashPaymentDataURL(paymentDataURL)
	if err != nil {
		return nil, err
	}
	message, err := decodeAppleCashPeerPaymentMessage(paymentData)
	if err != nil {
		return nil, err
	}
	message.SourceDisplayText, _ = root["ldtext"].(string)
	return &message, nil
}

func unarchiveAppleCashPropertyList(archive any) (map[string]any, error) {
	dictionary, ok := archive.(map[string]any)
	if !ok {
		return nil, errors.New("Apple Cash property list is not a dictionary")
	}
	objects, ok := dictionary["$objects"].([]any)
	if !ok {
		return nil, errors.New("Apple Cash property list has no object table")
	}
	top, ok := dictionary["$top"].(map[string]any)
	if !ok {
		return nil, errors.New("Apple Cash property list has no root")
	}
	rootUID, ok := top["root"].(plist.UID)
	if !ok {
		return nil, errors.New("Apple Cash property list root is not an object reference")
	}
	resolved, err := resolveAppleCashArchivedObject(objects, rootUID, 0)
	if err != nil {
		return nil, err
	}
	root, ok := resolved.(map[string]any)
	if !ok {
		return nil, errors.New("Apple Cash property list root object is not a dictionary")
	}
	return root, nil
}

func resolveAppleCashArchivedObject(objects []any, value any, depth int) (any, error) {
	if depth >= 256 {
		return nil, errors.New("Apple Cash property list object graph is too deep")
	}
	if uid, ok := value.(plist.UID); ok {
		index := uint64(uid)
		if index >= uint64(len(objects)) {
			return nil, fmt.Errorf("Apple Cash property list object reference %d is out of range", index)
		}
		return resolveAppleCashArchivedObject(objects, objects[index], depth+1)
	}
	switch typedValue := value.(type) {
	case []any:
		resolved := make([]any, 0, len(typedValue))
		for _, item := range typedValue {
			resolvedItem, err := resolveAppleCashArchivedObject(objects, item, depth+1)
			if err != nil {
				return nil, err
			}
			resolved = append(resolved, resolvedItem)
		}
		return resolved, nil
	case map[string]any:
		if relative, found := typedValue["NS.relative"]; found {
			return resolveAppleCashArchivedObject(objects, relative, depth+1)
		}
		if keysValue, hasKeys := typedValue["NS.keys"]; hasKeys {
			valuesValue, hasValues := typedValue["NS.objects"]
			if !hasValues {
				return nil, errors.New("Apple Cash archived dictionary has no values")
			}
			keys, ok := keysValue.([]any)
			if !ok {
				return nil, errors.New("Apple Cash archived dictionary keys are invalid")
			}
			values, ok := valuesValue.([]any)
			if !ok || len(keys) != len(values) {
				return nil, errors.New("Apple Cash archived dictionary values are invalid")
			}
			resolved := make(map[string]any, len(keys))
			for index := range keys {
				keyValue, err := resolveAppleCashArchivedObject(objects, keys[index], depth+1)
				if err != nil {
					return nil, err
				}
				key, ok := keyValue.(string)
				if !ok {
					return nil, errors.New("Apple Cash archived dictionary key is not text")
				}
				resolved[key], err = resolveAppleCashArchivedObject(objects, values[index], depth+1)
				if err != nil {
					return nil, err
				}
			}
			return resolved, nil
		}
		resolved := make(map[string]any, len(typedValue))
		for key, item := range typedValue {
			if key == "$class" {
				continue
			}
			resolvedItem, err := resolveAppleCashArchivedObject(objects, item, depth+1)
			if err != nil {
				return nil, err
			}
			resolved[key] = resolvedItem
		}
		return resolved, nil
	default:
		return value, nil
	}
}

func decodeAppleCashPaymentDataURL(value string) ([]byte, error) {
	header, encoded, found := strings.Cut(strings.TrimSpace(value), ",")
	if !found || !strings.HasPrefix(header, "data:application/vnd.apple.pkppm") {
		return nil, errors.New("Apple Cash payment data URL is missing")
	}
	if strings.HasSuffix(header, ";base64") {
		decoded, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil {
			return nil, fmt.Errorf("decode Apple Cash payment data URL: %w", err)
		}
		return decoded, nil
	}
	decoded, err := url.PathUnescape(encoded)
	if err != nil {
		return nil, fmt.Errorf("decode Apple Cash payment data URL: %w", err)
	}
	return []byte(decoded), nil
}

func decodeAppleCashPeerPaymentMessage(data []byte) (AppleCashMessage, error) {
	var message AppleCashMessage
	for len(data) > 0 {
		fieldNumber, wireType, consumed := protowire.ConsumeTag(data)
		if consumed < 0 {
			return AppleCashMessage{}, protowire.ParseError(consumed)
		}
		data = data[consumed:]
		switch {
		case fieldNumber == 1 || fieldNumber == 3 || fieldNumber == 5 || fieldNumber == 13 || fieldNumber == 21:
			value, fieldBytes := protowire.ConsumeVarint(data)
			if wireType != protowire.VarintType || fieldBytes < 0 {
				return AppleCashMessage{}, fmt.Errorf("Apple Cash payment field %d is invalid", fieldNumber)
			}
			switch fieldNumber {
			case 1:
				message.Version = uint32(value)
			case 3:
				message.Kind = AppleCashMessageKind(int32(value))
			case 5:
				message.LegacyAmount = int64(value)
			case 13:
				message.PaymentSource = AppleCashPaymentSource(int32(value))
			case 21:
				message.MessagesContext = AppleCashMessagesContext(int32(value))
			}
			data = data[fieldBytes:]
		case fieldNumber == 17:
			value, fieldBytes := protowire.ConsumeFixed64(data)
			if wireType != protowire.Fixed64Type || fieldBytes < 0 {
				return AppleCashMessage{}, errors.New("Apple Cash recurring payment start date is invalid")
			}
			message.RecurringPaymentStartDate = math.Float64frombits(value)
			data = data[fieldBytes:]
		case fieldNumber == 2 || fieldNumber == 4 || (fieldNumber >= 6 && fieldNumber <= 12) ||
			(fieldNumber >= 14 && fieldNumber <= 16) || fieldNumber == 18 || fieldNumber == 19 ||
			fieldNumber == 20 || fieldNumber == 22 || fieldNumber == 23:
			value, fieldBytes := protowire.ConsumeBytes(data)
			if wireType != protowire.BytesType || fieldBytes < 0 {
				return AppleCashMessage{}, fmt.Errorf("Apple Cash payment field %d is invalid", fieldNumber)
			}
			switch fieldNumber {
			case 2:
				message.Identifier = string(value)
			case 4:
				message.CurrencyCode = string(value)
			case 6:
				message.SenderAddress = string(value)
			case 7:
				message.RecipientAddress = string(value)
			case 8:
				message.RequestToken = string(value)
			case 9:
				message.PaymentIdentifier = string(value)
			case 10:
				message.TransactionIdentifier = string(value)
			case 11:
				message.Memo = string(value)
			case 12:
				message.RequestDeviceScoreIdentifier = string(value)
			case 14:
				message.RecurringPaymentIdentifier = string(value)
			case 15:
				message.RecurringPaymentEmoji = string(value)
			case 16:
				message.RecurringPaymentColor = string(value)
			case 18:
				message.RecurringPaymentFrequency = string(value)
			case 19:
				decimalAmount, err := decodeAppleCashDecimalAmount(value)
				if err != nil {
					return AppleCashMessage{}, err
				}
				message.DecimalAmount = &decimalAmount
			case 20:
				message.LocalData = append(message.LocalData[:0], value...)
			case 22:
				message.PaymentSignature = string(value)
			case 23:
				message.MessagesGroupIdentifier = string(value)
			}
			if fieldNumber != 19 && fieldNumber != 20 && !utf8.Valid(value) {
				return AppleCashMessage{}, fmt.Errorf("Apple Cash payment field %d is not UTF-8", fieldNumber)
			}
			data = data[fieldBytes:]
		default:
			fieldBytes := protowire.ConsumeFieldValue(fieldNumber, wireType, data)
			if fieldBytes < 0 {
				return AppleCashMessage{}, protowire.ParseError(fieldBytes)
			}
			data = data[fieldBytes:]
		}
	}
	return message, nil
}

func decodeAppleCashDecimalAmount(data []byte) (AppleCashDecimalAmount, error) {
	var amount AppleCashDecimalAmount
	for len(data) > 0 {
		fieldNumber, wireType, consumed := protowire.ConsumeTag(data)
		if consumed < 0 {
			return AppleCashDecimalAmount{}, protowire.ParseError(consumed)
		}
		data = data[consumed:]
		switch fieldNumber {
		case 7:
			value, fieldBytes := protowire.ConsumeBytes(data)
			if wireType != protowire.BytesType || fieldBytes < 0 {
				return AppleCashDecimalAmount{}, errors.New("Apple Cash decimal mantissa is invalid")
			}
			amount.Mantissa = append(amount.Mantissa[:0], value...)
			data = data[fieldBytes:]
		case 1, 2, 3, 4, 5, 6:
			value, fieldBytes := protowire.ConsumeVarint(data)
			if wireType != protowire.VarintType || fieldBytes < 0 {
				return AppleCashDecimalAmount{}, fmt.Errorf("Apple Cash decimal field %d is invalid", fieldNumber)
			}
			switch fieldNumber {
			case 1:
				amount.Version = uint32(value)
			case 2:
				amount.Exponent = int32(uint32(value))
			case 3:
				amount.Length = int32(uint32(value))
			case 4:
				amount.Negative = value != 0
			case 5:
				amount.Compact = value != 0
			case 6:
				amount.Reserved = int32(uint32(value))
			}
			data = data[fieldBytes:]
		default:
			fieldBytes := protowire.ConsumeFieldValue(fieldNumber, wireType, data)
			if fieldBytes < 0 {
				return AppleCashDecimalAmount{}, protowire.ParseError(fieldBytes)
			}
			data = data[fieldBytes:]
		}
	}
	return amount, nil
}
