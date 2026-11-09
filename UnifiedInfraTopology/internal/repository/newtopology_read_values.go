package repository

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strconv"
	"time"

	nebulago "github.com/vesoft-inc/nebula-go/v3"
	"github.com/vesoft-inc/nebula-go/v3/nebula"
)

func topologyReadDeclaredLists(schema topologyEntitySchema, props map[string]any) error {
	for key, propertyType := range schema.properties {
		if propertyType != topologyStringList && propertyType != topologyIntegerList {
			continue
		}
		value := props[key]
		if value == nil {
			continue
		}
		text, storedString := value.(string)
		if !storedString {
			if _, ok := value.([]any); !ok {
				return ErrInventoryNotReady
			}
			continue
		}
		var items []json.RawMessage
		if err := json.Unmarshal([]byte(text), &items); err != nil || items == nil {
			return ErrInventoryNotReady
		}
		decoded := make([]any, 0, len(items))
		for _, item := range items {
			if propertyType == topologyStringList {
				var text *string
				if err := json.Unmarshal(item, &text); err != nil || text == nil {
					return ErrInventoryNotReady
				}
				decoded = append(decoded, *text)
			} else {
				var integer *int64
				if err := json.Unmarshal(item, &integer); err != nil || integer == nil {
					return ErrInventoryNotReady
				}
				if *integer > 9007199254740991 || *integer < -9007199254740991 {
					decoded = append(decoded, strconv.FormatInt(*integer, 10))
				} else {
					decoded = append(decoded, *integer)
				}
			}
		}
		props[key] = decoded
	}
	return nil
}

func topologyReadProperties(value nebulago.ValueWrapper) (map[string]any, error) {
	if !value.IsMap() {
		return nil, ErrInventoryNotReady
	}
	decoded, err := topologyReadValue(value)
	if err != nil {
		return nil, err
	}
	return decoded.(map[string]any), nil
}

func topologyReadValue(value nebulago.ValueWrapper) (any, error) {
	switch {
	case value.IsNull():
		null, err := value.AsNull()
		if err != nil || null != nebula.NullType___NULL__ {
			return nil, ErrInventoryNotReady
		}
		return nil, nil
	case value.IsBool():
		return value.AsBool()
	case value.IsString():
		return value.AsString()
	case value.IsInt():
		integer, err := value.AsInt()
		if err != nil {
			return nil, ErrInventoryNotReady
		}
		if integer > 9007199254740991 || integer < -9007199254740991 {
			return strconv.FormatInt(integer, 10), nil
		}
		return integer, nil
	case value.IsFloat():
		number, err := value.AsFloat()
		if err != nil || math.IsNaN(number) || math.IsInf(number, 0) {
			return nil, ErrInventoryNotReady
		}
		return number, nil
	case value.IsDateTime():
		wrapper, err := value.AsDateTime()
		if err != nil {
			return nil, ErrInventoryNotReady
		}
		dt, err := wrapper.GetLocalDateTimeWithTimezoneName("UTC")
		if err != nil {
			return nil, ErrInventoryNotReady
		}
		stamp := time.Date(int(dt.Year), time.Month(dt.Month), int(dt.Day), int(dt.Hour), int(dt.Minute), int(dt.Sec), int(dt.Microsec)*1000, time.UTC)
		if stamp.Year() != int(dt.Year) || stamp.Month() != time.Month(dt.Month) || stamp.Day() != int(dt.Day) || stamp.Hour() != int(dt.Hour) || stamp.Minute() != int(dt.Minute) || stamp.Second() != int(dt.Sec) || dt.Microsec < 0 || dt.Microsec > 999999 {
			return nil, ErrInventoryNotReady
		}
		return stamp, nil
	case value.IsDate():
		date, err := value.AsDate()
		if err != nil {
			return nil, ErrInventoryNotReady
		}
		stamp := time.Date(int(date.Year), time.Month(date.Month), int(date.Day), 0, 0, 0, 0, time.UTC)
		if stamp.Year() != int(date.Year) || stamp.Month() != time.Month(date.Month) || stamp.Day() != int(date.Day) {
			return nil, ErrInventoryNotReady
		}
		return fmt.Sprintf("%04d-%02d-%02d", date.Year, date.Month, date.Day), nil
	case value.IsList(), value.IsSet():
		var list []nebulago.ValueWrapper
		var err error
		if value.IsList() {
			list, err = value.AsList()
		} else {
			list, err = value.AsDedupList()
		}
		if err != nil {
			return nil, ErrInventoryNotReady
		}
		out := make([]any, 0, len(list))
		for _, item := range list {
			decoded, err := topologyReadValue(item)
			if err != nil {
				return nil, err
			}
			out = append(out, decoded)
		}
		if value.IsSet() {
			sort.Slice(out, func(i, j int) bool {
				return fmt.Sprintf("%T:%v", out[i], out[i]) < fmt.Sprintf("%T:%v", out[j], out[j])
			})
		}
		return out, nil
	case value.IsMap():
		props, err := value.AsMap()
		if err != nil {
			return nil, ErrInventoryNotReady
		}
		out := make(map[string]any, len(props))
		for key, item := range props {
			decoded, err := topologyReadValue(item)
			if err != nil {
				return nil, err
			}
			out[key] = decoded
		}
		return out, nil
	default:
		return nil, ErrInventoryNotReady
	}
}
