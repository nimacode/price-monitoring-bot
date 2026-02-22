package database

import (
	"context"
	"log"
	"time"

	"price-monitoring-bot/internal/models"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
)

type Repository struct {
	db *MongoDB
}

func NewRepository(db *MongoDB) *Repository {
	return &Repository{db: db}
}

func (r *Repository) CreateAsset(asset *models.Asset) error {
	collection := r.db.Collection("assets")
	asset.CreatedAt = time.Now()
	asset.UpdatedAt = time.Now()
	
	result, err := collection.InsertOne(context.Background(), asset)
	if err != nil {
		return err
	}
	
	asset.ID = result.InsertedID.(primitive.ObjectID)
	return nil
}

func (r *Repository) GetAssetByID(id primitive.ObjectID) (*models.Asset, error) {
	collection := r.db.Collection("assets")
	var asset models.Asset
	
	err := collection.FindOne(context.Background(), bson.M{"_id": id}).Decode(&asset)
	if err != nil {
		return nil, err
	}
	
	return &asset, nil
}

func (r *Repository) GetAssetsByCategory(category models.AssetCategory) ([]models.Asset, error) {
	collection := r.db.Collection("assets")
	cursor, err := collection.Find(context.Background(), bson.M{"category": category})
	if err != nil {
		return nil, err
	}
	defer cursor.Close(context.Background())
	
	var assets []models.Asset
	if err := cursor.All(context.Background(), &assets); err != nil {
		return nil, err
	}
	
	return assets, nil
}

func (r *Repository) GetAllAssets() ([]models.Asset, error) {
	collection := r.db.Collection("assets")
	cursor, err := collection.Find(context.Background(), bson.M{})
	if err != nil {
		return nil, err
	}
	defer cursor.Close(context.Background())
	
	var assets []models.Asset
	if err := cursor.All(context.Background(), &assets); err != nil {
		return nil, err
	}
	
	return assets, nil
}

func (r *Repository) UpdateAssetLastSentPrice(id primitive.ObjectID, price float64) error {
	collection := r.db.Collection("assets")
	
	_, err := collection.UpdateOne(
		context.Background(),
		bson.M{"_id": id},
		bson.M{"$set": bson.M{"last_sent_price": price, "updated_at": time.Now()}},
	)
	
	return err
}

func (r *Repository) CreateSource(source *models.Source) error {
	collection := r.db.Collection("sources")
	source.UpdatedAt = time.Now()
	
	result, err := collection.InsertOne(context.Background(), source)
	if err != nil {
		return err
	}
	
	source.ID = result.InsertedID.(primitive.ObjectID)
	return nil
}

func (r *Repository) GetSourcesByAssetID(assetID primitive.ObjectID) ([]models.Source, error) {
	collection := r.db.Collection("sources")
	cursor, err := collection.Find(context.Background(), bson.M{"asset_id": assetID})
	if err != nil {
		return nil, err
	}
	defer cursor.Close(context.Background())
	
	var sources []models.Source
	if err := cursor.All(context.Background(), &sources); err != nil {
		return nil, err
	}
	
	return sources, nil
}

func (r *Repository) GetAllSources() ([]models.Source, error) {
	collection := r.db.Collection("sources")
	cursor, err := collection.Find(context.Background(), bson.M{})
	if err != nil {
		return nil, err
	}
	defer cursor.Close(context.Background())
	
	var sources []models.Source
	if err := cursor.All(context.Background(), &sources); err != nil {
		return nil, err
	}
	
	return sources, nil
}

func (r *Repository) UpdateSourceValue(id primitive.ObjectID, value float64) error {
	collection := r.db.Collection("sources")
	
	_, err := collection.UpdateOne(
		context.Background(),
		bson.M{"_id": id},
		bson.M{"$set": bson.M{"last_val": value, "updated_at": time.Now()}},
	)
	
	return err
}

func (r *Repository) GetSourcesByAssetIDWithObjectID(assetID string) ([]models.Source, error) {
	collection := r.db.Collection("sources")
	
	objectID, err := primitive.ObjectIDFromHex(assetID)
	if err != nil {
		return nil, err
	}
	
	cursor, err := collection.Find(context.Background(), bson.M{"asset_id": objectID})
	if err != nil {
		return nil, err
	}
	defer cursor.Close(context.Background())
	
	var sources []models.Source
	if err := cursor.All(context.Background(), &sources); err != nil {
		return nil, err
	}
	
	return sources, nil
}

func (r *Repository) GetSourcesWithAssets() ([]models.Source, error) {
	sources, err := r.GetAllSources()
	if err != nil {
		return nil, err
	}
	
	for i := range sources {
		asset, err := r.GetAssetByID(sources[i].AssetID)
		if err != nil {
			log.Printf("Error fetching asset %s: %v", sources[i].AssetID, err)
			continue
		}
		sources[i].AssetID = asset.ID
	}
	
	return sources, nil
}
