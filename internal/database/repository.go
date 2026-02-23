package database

import (
	"context"
	"time"

	"price-monitoring-bot/internal/models"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

type Repository struct {
	db *MongoDB
}

func NewRepository(db *MongoDB) *Repository {
	return &Repository{db: db}
}

func (r *Repository) CreateIndexes(ctx context.Context) error {
	assetIndexes := []mongo.IndexModel{
		{Keys: bson.D{{Key: "name", Value: 1}, {Key: "category", Value: 1}}, Options: options.Index().SetUnique(true)},
		{Keys: bson.D{{Key: "category", Value: 1}}},
	}
	if _, err := r.db.Collection("assets").Indexes().CreateMany(ctx, assetIndexes); err != nil {
		return err
	}

	sourceIndexes := []mongo.IndexModel{
		{Keys: bson.D{{Key: "asset_id", Value: 1}}},
		{Keys: bson.D{{Key: "source_name", Value: 1}}},
		{Keys: bson.D{{Key: "fetch_type", Value: 1}}},
	}
	if _, err := r.db.Collection("sources").Indexes().CreateMany(ctx, sourceIndexes); err != nil {
		return err
	}

	return nil
}

func (r *Repository) CreateAsset(ctx context.Context, asset *models.Asset) error {
	asset.CreatedAt = time.Now()
	asset.UpdatedAt = time.Now()

	result, err := r.db.Collection("assets").InsertOne(ctx, asset)
	if err != nil {
		return err
	}

	asset.ID = result.InsertedID.(primitive.ObjectID)
	return nil
}

func (r *Repository) GetAssetByID(ctx context.Context, id primitive.ObjectID) (*models.Asset, error) {
	var asset models.Asset
	err := r.db.Collection("assets").FindOne(ctx, bson.M{"_id": id}).Decode(&asset)
	if err != nil {
		return nil, err
	}
	return &asset, nil
}

func (r *Repository) GetAssetsByCategory(ctx context.Context, category models.AssetCategory) ([]models.Asset, error) {
	cursor, err := r.db.Collection("assets").Find(ctx, bson.M{"category": category})
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)

	var assets []models.Asset
	if err := cursor.All(ctx, &assets); err != nil {
		return nil, err
	}
	return assets, nil
}

func (r *Repository) GetAllAssets(ctx context.Context) ([]models.Asset, error) {
	cursor, err := r.db.Collection("assets").Find(ctx, bson.M{})
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)

	var assets []models.Asset
	if err := cursor.All(ctx, &assets); err != nil {
		return nil, err
	}
	return assets, nil
}

func (r *Repository) UpdateAssetLastSentPrice(ctx context.Context, id primitive.ObjectID, price float64) error {
	_, err := r.db.Collection("assets").UpdateOne(
		ctx,
		bson.M{"_id": id},
		bson.M{"$set": bson.M{"last_sent_price": price, "updated_at": time.Now()}},
	)
	return err
}

func (r *Repository) CreateSource(ctx context.Context, source *models.Source) error {
	source.UpdatedAt = time.Now()

	result, err := r.db.Collection("sources").InsertOne(ctx, source)
	if err != nil {
		return err
	}

	source.ID = result.InsertedID.(primitive.ObjectID)
	return nil
}

func (r *Repository) GetSourcesByAssetID(ctx context.Context, assetID primitive.ObjectID) ([]models.Source, error) {
	cursor, err := r.db.Collection("sources").Find(ctx, bson.M{"asset_id": assetID})
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)

	var sources []models.Source
	if err := cursor.All(ctx, &sources); err != nil {
		return nil, err
	}
	return sources, nil
}

func (r *Repository) GetAllSources(ctx context.Context) ([]models.Source, error) {
	cursor, err := r.db.Collection("sources").Find(ctx, bson.M{})
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)

	var sources []models.Source
	if err := cursor.All(ctx, &sources); err != nil {
		return nil, err
	}
	return sources, nil
}

func (r *Repository) GetSourceByID(ctx context.Context, id primitive.ObjectID) (*models.Source, error) {
	var source models.Source
	err := r.db.Collection("sources").FindOne(ctx, bson.M{"_id": id}).Decode(&source)
	if err != nil {
		return nil, err
	}
	return &source, nil
}

func (r *Repository) UpdateSourceValue(ctx context.Context, id primitive.ObjectID, value float64) error {
	_, err := r.db.Collection("sources").UpdateOne(
		ctx,
		bson.M{"_id": id},
		bson.M{"$set": bson.M{"last_val": value, "updated_at": time.Now()}},
	)
	return err
}

func (r *Repository) UpdateSourceField(ctx context.Context, id primitive.ObjectID, field string, value interface{}) error {
	_, err := r.db.Collection("sources").UpdateOne(
		ctx,
		bson.M{"_id": id},
		bson.M{"$set": bson.M{field: value, "updated_at": time.Now()}},
	)
	return err
}

func (r *Repository) DeleteSource(ctx context.Context, id primitive.ObjectID) error {
	_, err := r.db.Collection("sources").DeleteOne(ctx, bson.M{"_id": id})
	return err
}

func (r *Repository) GetSourcesByAssetIDHex(ctx context.Context, assetIDHex string) ([]models.Source, error) {
	objectID, err := primitive.ObjectIDFromHex(assetIDHex)
	if err != nil {
		return nil, err
	}
	return r.GetSourcesByAssetID(ctx, objectID)
}
