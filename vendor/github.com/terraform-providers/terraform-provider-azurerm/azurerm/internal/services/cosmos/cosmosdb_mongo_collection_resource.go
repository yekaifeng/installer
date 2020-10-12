package cosmos

import (
	"fmt"
	"log"
	"strconv"
	"time"

	"github.com/Azure/azure-sdk-for-go/services/cosmos-db/mgmt/2015-04-08/documentdb"
	"github.com/hashicorp/go-azure-helpers/response"
	"github.com/hashicorp/terraform-plugin-sdk/helper/schema"
	"github.com/hashicorp/terraform-plugin-sdk/helper/validation"
	"github.com/terraform-providers/terraform-provider-azurerm/azurerm/helpers/azure"
	"github.com/terraform-providers/terraform-provider-azurerm/azurerm/helpers/tf"
	"github.com/terraform-providers/terraform-provider-azurerm/azurerm/helpers/validate"
	"github.com/terraform-providers/terraform-provider-azurerm/azurerm/internal/clients"
	"github.com/terraform-providers/terraform-provider-azurerm/azurerm/internal/features"
	"github.com/terraform-providers/terraform-provider-azurerm/azurerm/internal/timeouts"
	"github.com/terraform-providers/terraform-provider-azurerm/azurerm/utils"
)

func resourceArmCosmosDbMongoCollection() *schema.Resource {
	return &schema.Resource{
		Create: resourceArmCosmosDbMongoCollectionCreate,
		Read:   resourceArmCosmosDbMongoCollectionRead,
		Update: resourceArmCosmosDbMongoCollectionUpdate,
		Delete: resourceArmCosmosDbMongoCollectionDelete,

		Importer: &schema.ResourceImporter{
			State: schema.ImportStatePassthrough,
		},

		Timeouts: &schema.ResourceTimeout{
			Create: schema.DefaultTimeout(30 * time.Minute),
			Read:   schema.DefaultTimeout(5 * time.Minute),
			Update: schema.DefaultTimeout(30 * time.Minute),
			Delete: schema.DefaultTimeout(30 * time.Minute),
		},

		Schema: map[string]*schema.Schema{
			"name": {
				Type:         schema.TypeString,
				Required:     true,
				ForceNew:     true,
				ValidateFunc: validate.CosmosEntityName,
			},

			"resource_group_name": azure.SchemaResourceGroupName(),

			"account_name": {
				Type:         schema.TypeString,
				Required:     true,
				ForceNew:     true,
				ValidateFunc: validate.CosmosAccountName,
			},

			"database_name": {
				Type:         schema.TypeString,
				Required:     true,
				ForceNew:     true,
				ValidateFunc: validate.CosmosEntityName,
			},

			// SDK/api accepts an array.. but only one is allowed
			"shard_key": {
				Type:         schema.TypeString,
				Optional:     true,
				ForceNew:     true,
				ValidateFunc: validation.StringIsNotEmpty,
			},

			// default TTL is simply an index on _ts with expireAfterOption, given we can't seem to set TTLs on a given index lets expose this to match the portal
			"default_ttl_seconds": {
				Type:         schema.TypeInt,
				Optional:     true,
				ValidateFunc: validation.IntAtLeast(-1),
			},

			"throughput": {
				Type:         schema.TypeInt,
				Optional:     true,
				Computed:     true,
				ValidateFunc: validate.CosmosThroughput,
			},

			"index": {
				Type:     schema.TypeSet,
				Optional: true,
				Elem: &schema.Resource{
					Schema: map[string]*schema.Schema{
						"keys": {
							Type:     schema.TypeSet,
							Required: true,
							Elem:     &schema.Schema{Type: schema.TypeString},
						},

						"unique": {
							Type:     schema.TypeBool,
							Optional: true,
							Default:  false,
						},
					},
				},
			},

			"system_indexes": {
				Type:     schema.TypeList,
				Computed: true,
				Elem: &schema.Resource{
					Schema: map[string]*schema.Schema{
						"keys": {
							Type:     schema.TypeList,
							Computed: true,
							Elem:     &schema.Schema{Type: schema.TypeString},
						},

						"unique": {
							Type:     schema.TypeBool,
							Computed: true,
						},
					},
				},
			},
		},
	}
}

func resourceArmCosmosDbMongoCollectionCreate(d *schema.ResourceData, meta interface{}) error {
	client := meta.(*clients.Client).Cosmos.DatabaseClient
	ctx, cancel := timeouts.ForCreateUpdate(meta.(*clients.Client).StopContext, d)
	defer cancel()

	name := d.Get("name").(string)
	resourceGroup := d.Get("resource_group_name").(string)
	account := d.Get("account_name").(string)
	database := d.Get("database_name").(string)

	if features.ShouldResourcesBeImported() {
		existing, err := client.GetMongoDBCollection(ctx, resourceGroup, account, database, name)
		if err != nil {
			if !utils.ResponseWasNotFound(existing.Response) {
				return fmt.Errorf("Error checking for presence of creating Cosmos Mongo Collection %s (Account %s, Database %s): %+v", name, account, database, err)
			}
		} else {
			id, err := azure.CosmosGetIDFromResponse(existing.Response)
			if err != nil {
				return fmt.Errorf("Error generating import ID for Cosmos Mongo Collection %s (Account %s, Database %s)", name, account, database)
			}

			return tf.ImportAsExistsError("azurerm_cosmosdb_mongo_collection", id)
		}
	}

	var ttl *int
	if v := d.Get("default_ttl_seconds").(int); v > 0 {
		ttl = utils.Int(v)
	}

	db := documentdb.MongoDBCollectionCreateUpdateParameters{
		MongoDBCollectionCreateUpdateProperties: &documentdb.MongoDBCollectionCreateUpdateProperties{
			Resource: &documentdb.MongoDBCollectionResource{
				ID:      &name,
				Indexes: expandCosmosMongoCollectionIndex(d.Get("index").(*schema.Set).List(), ttl),
			},
			Options: map[string]*string{},
		},
	}

	if throughput, hasThroughput := d.GetOk("throughput"); hasThroughput {
		db.MongoDBCollectionCreateUpdateProperties.Options = map[string]*string{
			"throughput": utils.String(strconv.Itoa(throughput.(int))),
		}
	}

	if shardKey := d.Get("shard_key").(string); shardKey != "" {
		db.MongoDBCollectionCreateUpdateProperties.Resource.ShardKey = map[string]*string{
			shardKey: utils.String("Hash"), // looks like only hash is supported for now
		}
	}

	future, err := client.CreateUpdateMongoDBCollection(ctx, resourceGroup, account, database, name, db)
	if err != nil {
		return fmt.Errorf("Error issuing create/update request for Cosmos Mongo Collection %s (Account %s, Database %s): %+v", name, account, database, err)
	}

	if err = future.WaitForCompletionRef(ctx, client.Client); err != nil {
		return fmt.Errorf("Error waiting on create/update future for Cosmos Mongo Collection %s (Account %s, Database %s): %+v", name, account, database, err)
	}

	resp, err := client.GetMongoDBCollection(ctx, resourceGroup, account, database, name)
	if err != nil {
		return fmt.Errorf("Error making get request for Cosmos Mongo Collection %s (Account %s, Database %s): %+v", name, account, database, err)
	}

	id, err := azure.CosmosGetIDFromResponse(resp.Response)
	if err != nil {
		return fmt.Errorf("Error getting ID for Cosmos Mongo Collection %s (Account %s, Database %s) ID: %v", name, account, database, err)
	}
	d.SetId(id)

	return resourceArmCosmosDbMongoCollectionRead(d, meta)
}

func resourceArmCosmosDbMongoCollectionUpdate(d *schema.ResourceData, meta interface{}) error {
	client := meta.(*clients.Client).Cosmos.DatabaseClient
	ctx, cancel := timeouts.ForCreateUpdate(meta.(*clients.Client).StopContext, d)
	defer cancel()

	id, err := azure.ParseCosmosDatabaseCollectionID(d.Id())
	if err != nil {
		return err
	}

	var ttl *int
	if v := d.Get("default_ttl_seconds").(int); v > 0 {
		ttl = utils.Int(v)
	}

	db := documentdb.MongoDBCollectionCreateUpdateParameters{
		MongoDBCollectionCreateUpdateProperties: &documentdb.MongoDBCollectionCreateUpdateProperties{
			Resource: &documentdb.MongoDBCollectionResource{
				ID:      &id.Collection,
				Indexes: expandCosmosMongoCollectionIndex(d.Get("index").(*schema.Set).List(), ttl),
			},
			Options: map[string]*string{},
		},
	}

	if shardKey := d.Get("shard_key").(string); shardKey != "" {
		db.MongoDBCollectionCreateUpdateProperties.Resource.ShardKey = map[string]*string{
			shardKey: utils.String("Hash"), // looks like only hash is supported for now
		}
	}

	future, err := client.CreateUpdateMongoDBCollection(ctx, id.ResourceGroup, id.Account, id.Database, id.Collection, db)
	if err != nil {
		return fmt.Errorf("Error issuing create/update request for Cosmos Mongo Collection %s (Account %s, Database %s): %+v", id.Collection, id.Account, id.Database, err)
	}

	if err = future.WaitForCompletionRef(ctx, client.Client); err != nil {
		return fmt.Errorf("Error waiting on create/update future for Cosmos Mongo Collection %s (Account %s, Database %s): %+v", id.Collection, id.Account, id.Database, err)
	}

	if d.HasChange("throughput") {
		throughputParameters := documentdb.ThroughputUpdateParameters{
			ThroughputUpdateProperties: &documentdb.ThroughputUpdateProperties{
				Resource: &documentdb.ThroughputResource{
					Throughput: utils.Int32(int32(d.Get("throughput").(int))),
				},
			},
		}

		throughputFuture, err := client.UpdateMongoDBCollectionThroughput(ctx, id.ResourceGroup, id.Account, id.Database, id.Collection, throughputParameters)
		if err != nil {
			if response.WasNotFound(throughputFuture.Response()) {
				return fmt.Errorf("Error setting Throughput for Cosmos MongoDB Collection %s (Account %s, Database %s): %+v - "+
					"If the collection has not been created with an initial throughput, you cannot configure it later.", id.Collection, id.Account, id.Database, err)
			}
		}

		if err = throughputFuture.WaitForCompletionRef(ctx, client.Client); err != nil {
			return fmt.Errorf("Error waiting on ThroughputUpdate future for Cosmos Mongo Collection %s (Account %s, Database %s): %+v", id.Collection, id.Account, id.Database, err)
		}
	}

	return resourceArmCosmosDbMongoCollectionRead(d, meta)
}

func resourceArmCosmosDbMongoCollectionRead(d *schema.ResourceData, meta interface{}) error {
	client := meta.(*clients.Client).Cosmos.DatabaseClient
	ctx, cancel := timeouts.ForRead(meta.(*clients.Client).StopContext, d)
	defer cancel()

	id, err := azure.ParseCosmosDatabaseCollectionID(d.Id())
	if err != nil {
		return err
	}

	resp, err := client.GetMongoDBCollection(ctx, id.ResourceGroup, id.Account, id.Database, id.Collection)
	if err != nil {
		if utils.ResponseWasNotFound(resp.Response) {
			log.Printf("[INFO] Error reading Cosmos Mongo Collection %s (Account %s, Database %s)", id.Collection, id.Account, id.Database)
			d.SetId("")
			return nil
		}

		return fmt.Errorf("Error reading Cosmos Mongo Collection %s (Account %s, Database %s): %+v", id.Collection, id.Account, id.Database, err)
	}

	d.Set("resource_group_name", id.ResourceGroup)
	d.Set("account_name", id.Account)
	d.Set("database_name", id.Database)
	if props := resp.MongoDBCollectionProperties; props != nil {
		d.Set("name", props.ID)

		// you can only have one
		if len(props.ShardKey) > 2 {
			return fmt.Errorf("unexpected number of shard keys: %d", len(props.ShardKey))
		}

		for k := range props.ShardKey {
			d.Set("shard_key", k)
		}

		indexes, systemIndexes, ttl := flattenCosmosMongoCollectionIndex(props.Indexes)
		if err := d.Set("default_ttl_seconds", ttl); err != nil {
			return fmt.Errorf("failed to set `default_ttl_seconds`: %+v", err)
		}
		if err := d.Set("index", indexes); err != nil {
			return fmt.Errorf("failed to set `index`: %+v", err)
		}
		if err := d.Set("system_indexes", systemIndexes); err != nil {
			return fmt.Errorf("failed to set `system_indexes`: %+v", err)
		}
	}

	throughputResp, err := client.GetMongoDBCollectionThroughput(ctx, id.ResourceGroup, id.Account, id.Database, id.Collection)
	if err != nil {
		if !utils.ResponseWasNotFound(throughputResp.Response) {
			return fmt.Errorf("Error reading Throughput on Cosmos Mongo Collection %s (Account %s, Database %s): %+v", id.Collection, id.Account, id.Database, err)
		} else {
			d.Set("throughput", nil)
		}
	} else {
		d.Set("throughput", throughputResp.Throughput)
	}

	return nil
}

func resourceArmCosmosDbMongoCollectionDelete(d *schema.ResourceData, meta interface{}) error {
	client := meta.(*clients.Client).Cosmos.DatabaseClient
	ctx, cancel := timeouts.ForDelete(meta.(*clients.Client).StopContext, d)
	defer cancel()

	id, err := azure.ParseCosmosDatabaseCollectionID(d.Id())
	if err != nil {
		return err
	}

	future, err := client.DeleteMongoDBCollection(ctx, id.ResourceGroup, id.Account, id.Database, id.Collection)
	if err != nil {
		if !response.WasNotFound(future.Response()) {
			return fmt.Errorf("Error deleting Cosmos Mongo Collection %s (Account %s, Database %s): %+v", id.Collection, id.Account, id.Database, err)
		}
	}

	err = future.WaitForCompletionRef(ctx, client.Client)
	if err != nil {
		return fmt.Errorf("Error waiting on delete future for Cosmos Mongo Collection %s (Account %s, Database %s): %+v", id.Collection, id.Account, id.Database, err)
	}

	return nil
}

func expandCosmosMongoCollectionIndex(indexes []interface{}, defaultTtl *int) *[]documentdb.MongoIndex {
	results := make([]documentdb.MongoIndex, 0)

	if len(indexes) != 0 {
		for _, v := range indexes {
			index := v.(map[string]interface{})

			results = append(results, documentdb.MongoIndex{
				Key: &documentdb.MongoIndexKeys{
					Keys: utils.ExpandStringSlice(index["keys"].(*schema.Set).List()),
				},
				Options: &documentdb.MongoIndexOptions{
					Unique: utils.Bool(index["unique"].(bool)),
				},
			})
		}
	}

	if defaultTtl != nil {
		results = append(results, documentdb.MongoIndex{
			Key: &documentdb.MongoIndexKeys{
				Keys: &[]string{"_ts"},
			},
			Options: &documentdb.MongoIndexOptions{
				ExpireAfterSeconds: utils.Int32(int32(*defaultTtl)),
			},
		})
	}

	return &results
}

func flattenCosmosMongoCollectionIndex(input *[]documentdb.MongoIndex) (*[]map[string]interface{}, *[]map[string]interface{}, *int32) {
	indexes := make([]map[string]interface{}, 0)
	systemIndexes := make([]map[string]interface{}, 0)
	var ttl *int32
	if input == nil {
		return &indexes, &systemIndexes, ttl
	}

	for _, v := range *input {
		index := map[string]interface{}{}
		systemIndex := map[string]interface{}{}

		if v.Key != nil && v.Key.Keys != nil && len(*v.Key.Keys) > 0 {
			key := (*v.Key.Keys)[0]

			switch key {
			// As `DocumentDBDefaultIndex` and `_id` cannot be updated, so they would be moved into `system_indexes`.
			case "_id":
				systemIndex["keys"] = utils.FlattenStringSlice(v.Key.Keys)
				// The system index `_id` is always unique but api returns nil and it would be converted to `false` by zero-value. So it has to be manually set as `true`.
				systemIndex["unique"] = true

				systemIndexes = append(systemIndexes, systemIndex)
			case "DocumentDBDefaultIndex":
				// Updating system index `DocumentDBDefaultIndex` is not a supported scenario.
				systemIndex["keys"] = utils.FlattenStringSlice(v.Key.Keys)

				isUnique := false
				if v.Options != nil && v.Options.Unique != nil {
					isUnique = *v.Options.Unique
				}
				systemIndex["unique"] = isUnique

				systemIndexes = append(systemIndexes, systemIndex)
			case "_ts":
				if v.Options != nil && v.Options.ExpireAfterSeconds != nil {
					// As `ExpireAfterSeconds` only can be applied to system index `_ts`, so it would be set in `default_ttl_seconds`.
					ttl = v.Options.ExpireAfterSeconds
				}
			default:
				// The other settable indexes would be set in `index`
				index["keys"] = utils.FlattenStringSlice(v.Key.Keys)

				isUnique := false
				if v.Options != nil && v.Options.Unique != nil {
					isUnique = *v.Options.Unique
				}
				index["unique"] = isUnique

				indexes = append(indexes, index)
			}
		}
	}

	return &indexes, &systemIndexes, ttl
}
