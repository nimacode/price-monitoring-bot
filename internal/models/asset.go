package models

import (
	"fmt"
	"strconv"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/bsontype"
	"go.mongodb.org/mongo-driver/bson/primitive"
)

// FlexFloat64 decodes both BSON double and Decimal128 into a float64.
type FlexFloat64 float64

func (f *FlexFloat64) UnmarshalBSONValue(t bsontype.Type, raw []byte) error {
	rv := bson.RawValue{Type: t, Value: raw}
	switch t {
	case bsontype.Double:
		*f = FlexFloat64(rv.Double())
	case bsontype.Decimal128:
		d := rv.Decimal128()
		v, err := strconv.ParseFloat(d.String(), 64)
		if err != nil {
			return fmt.Errorf("failed to parse Decimal128 %q: %w", d.String(), err)
		}
		*f = FlexFloat64(v)
	case bsontype.Int32:
		*f = FlexFloat64(rv.Int32())
	case bsontype.Int64:
		*f = FlexFloat64(rv.Int64())
	default:
		return fmt.Errorf("unsupported BSON type for FlexFloat64: %s", t)
	}
	return nil
}

type AssetCategory string

const (
	CurrencyCategory AssetCategory = "currency"
	GoldCategory     AssetCategory = "gold"
	CryptoCategory   AssetCategory = "crypto"
)

type Asset struct {
	ID            primitive.ObjectID `bson:"_id,omitempty" json:"id"`
	Name          string            `bson:"name" json:"name"`
	Category      AssetCategory     `bson:"category" json:"category"`
	AlertThreshold FlexFloat64      `bson:"alert_threshold" json:"alert_threshold"`
	LastSentPrice FlexFloat64       `bson:"last_sent_price" json:"last_sent_price"`
	CreatedAt     time.Time         `bson:"created_at" json:"created_at"`
	UpdatedAt     time.Time         `bson:"updated_at" json:"updated_at"`
}

type Source struct {
	ID         primitive.ObjectID `bson:"_id,omitempty" json:"id"`
	AssetID    primitive.ObjectID `bson:"asset_id" json:"asset_id"`
	SourceName string             `bson:"source_name" json:"source_name"`
	FetchType  string             `bson:"fetch_type" json:"fetch_type"`
	URL        string             `bson:"url" json:"url"`
	Selector   string             `bson:"selector" json:"selector"`
	Multiplier FlexFloat64        `bson:"multiplier" json:"multiplier"`
	LastVal    FlexFloat64        `bson:"last_val" json:"last_val"`
	UpdatedAt  time.Time          `bson:"updated_at" json:"updated_at"`
}

type PriceData struct {
	AssetName  string
	Category   AssetCategory
	Price      float64
	SourceName string
	UpdatedAt  time.Time
}
