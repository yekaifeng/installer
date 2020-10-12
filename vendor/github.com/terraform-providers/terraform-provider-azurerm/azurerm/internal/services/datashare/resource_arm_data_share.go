package datashare

import (
	"fmt"
	"log"
	"time"

	"github.com/Azure/azure-sdk-for-go/services/datashare/mgmt/2019-11-01/datashare"
	"github.com/Azure/go-autorest/autorest/date"
	"github.com/hashicorp/terraform-plugin-sdk/helper/schema"
	"github.com/hashicorp/terraform-plugin-sdk/helper/validation"
	"github.com/terraform-providers/terraform-provider-azurerm/azurerm/helpers/suppress"
	"github.com/terraform-providers/terraform-provider-azurerm/azurerm/helpers/tf"
	"github.com/terraform-providers/terraform-provider-azurerm/azurerm/internal/clients"
	"github.com/terraform-providers/terraform-provider-azurerm/azurerm/internal/services/datashare/parse"
	"github.com/terraform-providers/terraform-provider-azurerm/azurerm/internal/services/datashare/validate"
	azSchema "github.com/terraform-providers/terraform-provider-azurerm/azurerm/internal/tf/schema"
	"github.com/terraform-providers/terraform-provider-azurerm/azurerm/internal/timeouts"
	"github.com/terraform-providers/terraform-provider-azurerm/azurerm/utils"
)

func resourceArmDataShare() *schema.Resource {
	return &schema.Resource{
		Create: resourceArmDataShareCreateUpdate,
		Read:   resourceArmDataShareRead,
		Update: resourceArmDataShareCreateUpdate,
		Delete: resourceArmDataShareDelete,

		Timeouts: &schema.ResourceTimeout{
			Create: schema.DefaultTimeout(30 * time.Minute),
			Read:   schema.DefaultTimeout(5 * time.Minute),
			Update: schema.DefaultTimeout(30 * time.Minute),
			Delete: schema.DefaultTimeout(30 * time.Minute),
		},

		Importer: azSchema.ValidateResourceIDPriorToImport(func(id string) error {
			_, err := parse.DataShareID(id)
			return err
		}),

		Schema: map[string]*schema.Schema{
			"name": {
				Type:         schema.TypeString,
				Required:     true,
				ForceNew:     true,
				ValidateFunc: validate.DatashareName(),
			},

			"account_id": {
				Type:         schema.TypeString,
				Required:     true,
				ForceNew:     true,
				ValidateFunc: validate.DatashareAccountID,
			},

			"kind": {
				Type:     schema.TypeString,
				Required: true,
				ForceNew: true,
				ValidateFunc: validation.StringInSlice([]string{
					string(datashare.CopyBased),
					string(datashare.InPlace),
				}, false),
			},

			"description": {
				Type:     schema.TypeString,
				Optional: true,
			},

			"snapshot_schedule": {
				Type:     schema.TypeList,
				Optional: true,
				MaxItems: 1,
				Elem: &schema.Resource{
					Schema: map[string]*schema.Schema{
						"name": {
							Type:         schema.TypeString,
							Required:     true,
							ValidateFunc: validate.DataShareSyncName(),
						},

						"recurrence": {
							Type:     schema.TypeString,
							Required: true,
							ValidateFunc: validation.StringInSlice([]string{
								string(datashare.Day),
								string(datashare.Hour),
							}, false),
						},

						"start_time": {
							Type:             schema.TypeString,
							Required:         true,
							ValidateFunc:     validation.IsRFC3339Time,
							DiffSuppressFunc: suppress.RFC3339Time,
						},
					},
				},
			},

			"terms": {
				Type:     schema.TypeString,
				Optional: true,
			},
		},
	}
}
func resourceArmDataShareCreateUpdate(d *schema.ResourceData, meta interface{}) error {
	client := meta.(*clients.Client).DataShare.SharesClient
	syncClient := meta.(*clients.Client).DataShare.SynchronizationClient
	ctx, cancel := timeouts.ForCreateUpdate(meta.(*clients.Client).StopContext, d)
	defer cancel()

	name := d.Get("name").(string)
	accountId, err := parse.DataShareAccountID(d.Get("account_id").(string))
	if err != nil {
		return err
	}

	if d.IsNewResource() {
		existing, err := client.Get(ctx, accountId.ResourceGroup, accountId.Name, name)
		if err != nil {
			if !utils.ResponseWasNotFound(existing.Response) {
				return fmt.Errorf("checking for present of existing DataShare %q (Resource Group %q / accountName %q): %+v", name, accountId.ResourceGroup, accountId.Name, err)
			}
		}
		if existing.ID != nil && *existing.ID != "" {
			return tf.ImportAsExistsError("azurerm_data_share", *existing.ID)
		}
	}

	share := datashare.Share{
		ShareProperties: &datashare.ShareProperties{
			ShareKind:   datashare.ShareKind(d.Get("kind").(string)),
			Description: utils.String(d.Get("description").(string)),
			Terms:       utils.String(d.Get("terms").(string)),
		},
	}

	if _, err := client.Create(ctx, accountId.ResourceGroup, accountId.Name, name, share); err != nil {
		return fmt.Errorf("creating DataShare %q (Resource Group %q / accountName %q): %+v", name, accountId.ResourceGroup, accountId.Name, err)
	}

	resp, err := client.Get(ctx, accountId.ResourceGroup, accountId.Name, name)
	if err != nil {
		return fmt.Errorf("retrieving DataShare %q (Resource Group %q / accountName %q): %+v", name, accountId.ResourceGroup, accountId.Name, err)
	}

	if resp.ID == nil || *resp.ID == "" {
		return fmt.Errorf("reading DataShare %q (Resource Group %q / accountName %q): ID is empty", name, accountId.ResourceGroup, accountId.Name)
	}

	d.SetId(*resp.ID)

	if d.HasChange("snapshot_schedule") {
		// only one dependent sync setting is allowed in one data share
		o, _ := d.GetChange("snapshot_schedule")
		if origins := o.([]interface{}); len(origins) > 0 {
			origin := origins[0].(map[string]interface{})
			if originName, ok := origin["name"].(string); ok && originName != "" {
				syncFuture, err := syncClient.Delete(ctx, accountId.ResourceGroup, accountId.Name, name, originName)
				if err != nil {
					return fmt.Errorf("deleting DataShare %q snapshot schedule (Resource Group %q / accountName %q): %+v", name, accountId.ResourceGroup, accountId.Name, err)
				}
				if err = syncFuture.WaitForCompletionRef(ctx, syncClient.Client); err != nil {
					return fmt.Errorf("waiting for DataShare %q snapshot schedule (Resource Group %q / accountName %q) to be deleted: %+v", name, accountId.ResourceGroup, accountId.Name, err)
				}
			}
		}
	}

	if snapshotSchedule := expandAzureRmDataShareSnapshotSchedule(d.Get("snapshot_schedule").([]interface{})); snapshotSchedule != nil {
		if _, err := syncClient.Create(ctx, accountId.ResourceGroup, accountId.Name, name, d.Get("snapshot_schedule.0.name").(string), snapshotSchedule); err != nil {
			return fmt.Errorf("creating DataShare %q snapshot schedule (Resource Group %q / accountName %q): %+v", name, accountId.ResourceGroup, accountId.Name, err)
		}
	}

	return resourceArmDataShareRead(d, meta)
}

func resourceArmDataShareRead(d *schema.ResourceData, meta interface{}) error {
	client := meta.(*clients.Client).DataShare.SharesClient
	accountClient := meta.(*clients.Client).DataShare.AccountClient
	syncClient := meta.(*clients.Client).DataShare.SynchronizationClient
	ctx, cancel := timeouts.ForRead(meta.(*clients.Client).StopContext, d)
	defer cancel()

	id, err := parse.DataShareID(d.Id())
	if err != nil {
		return err
	}

	resp, err := client.Get(ctx, id.ResourceGroup, id.AccountName, id.Name)
	if err != nil {
		if utils.ResponseWasNotFound(resp.Response) {
			log.Printf("[INFO] DataShare %q does not exist - removing from state", d.Id())
			d.SetId("")
			return nil
		}
		return fmt.Errorf("retrieving DataShare %q (Resource Group %q / accountName %q): %+v", id.Name, id.ResourceGroup, id.AccountName, err)
	}

	accountResp, err := accountClient.Get(ctx, id.ResourceGroup, id.AccountName)
	if err != nil {
		return fmt.Errorf("retrieving DataShare Account %q (Resource Group %q): %+v", id.AccountName, id.ResourceGroup, err)
	}
	if accountResp.ID == nil || *accountResp.ID == "" {
		return fmt.Errorf("reading DataShare Account %q (Resource Group %q): ID is empty", id.AccountName, id.ResourceGroup)
	}

	d.Set("name", id.Name)
	d.Set("account_id", accountResp.ID)

	if props := resp.ShareProperties; props != nil {
		d.Set("kind", props.ShareKind)
		d.Set("description", props.Description)
		d.Set("terms", props.Terms)
	}

	if syncIterator, err := syncClient.ListByShareComplete(ctx, id.ResourceGroup, id.AccountName, id.Name, ""); syncIterator.NotDone() {
		if err != nil {
			return fmt.Errorf("listing DataShare %q snapshot schedule (Resource Group %q / accountName %q): %+v", id.Name, id.ResourceGroup, id.AccountName, err)
		}
		if syncName := syncIterator.Value().(datashare.ScheduledSynchronizationSetting).Name; syncName != nil && *syncName != "" {
			syncResp, err := syncClient.Get(ctx, id.ResourceGroup, id.AccountName, id.Name, *syncName)
			if err != nil {
				return fmt.Errorf("reading DataShare %q snapshot schedule (Resource Group %q / accountName %q): %+v", id.Name, id.ResourceGroup, id.AccountName, err)
			}
			if schedule := syncResp.Value.(datashare.ScheduledSynchronizationSetting); schedule.ID != nil && *schedule.ID != "" {
				if err := d.Set("snapshot_schedule", flattenAzureRmDataShareSnapshotSchedule(&schedule)); err != nil {
					return fmt.Errorf("setting `snapshot_schedule`: %+v", err)
				}
			}
		}
		if err := syncIterator.NextWithContext(ctx); err != nil {
			return fmt.Errorf("listing DataShare %q snapshot schedule (Resource Group %q / accountName %q): %+v", id.Name, id.ResourceGroup, id.AccountName, err)
		}
		if syncIterator.NotDone() {
			return fmt.Errorf("more than one DataShare %q snapshot schedule (Resource Group %q / accountName %q) is returned", id.Name, id.ResourceGroup, id.AccountName)
		}
	}

	return nil
}

func resourceArmDataShareDelete(d *schema.ResourceData, meta interface{}) error {
	client := meta.(*clients.Client).DataShare.SharesClient
	syncClient := meta.(*clients.Client).DataShare.SynchronizationClient
	ctx, cancel := timeouts.ForDelete(meta.(*clients.Client).StopContext, d)
	defer cancel()

	id, err := parse.DataShareID(d.Id())
	if err != nil {
		return err
	}

	// sync setting will not automatically be deleted after the data share is deleted
	if _, ok := d.GetOk("snapshot_schedule"); ok {
		syncFuture, err := syncClient.Delete(ctx, id.ResourceGroup, id.AccountName, id.Name, d.Get("snapshot_schedule.0.name").(string))
		if err != nil {
			return fmt.Errorf("deleting DataShare %q snapshot schedule (Resource Group %q / accountName %q): %+v", id.Name, id.ResourceGroup, id.AccountName, err)
		}
		if err = syncFuture.WaitForCompletionRef(ctx, syncClient.Client); err != nil {
			return fmt.Errorf("waiting for DataShare %q snapshot schedule (Resource Group %q / accountName %q) to be deleted: %+v", id.Name, id.ResourceGroup, id.AccountName, err)
		}
	}

	future, err := client.Delete(ctx, id.ResourceGroup, id.AccountName, id.Name)
	if err != nil {
		return fmt.Errorf("deleting DataShare %q (Resource Group %q / accountName %q): %+v", id.Name, id.ResourceGroup, id.AccountName, err)
	}

	if err = future.WaitForCompletionRef(ctx, client.Client); err != nil {
		return fmt.Errorf("waiting for DataShare %q (Resource Group %q / accountName %q) to be deleted: %+v", id.Name, id.ResourceGroup, id.AccountName, err)
	}

	return nil
}

func expandAzureRmDataShareSnapshotSchedule(input []interface{}) *datashare.ScheduledSynchronizationSetting {
	if len(input) == 0 {
		return nil
	}

	snapshotSchedule := input[0].(map[string]interface{})

	startTime, _ := time.Parse(time.RFC3339, snapshotSchedule["start_time"].(string))

	return &datashare.ScheduledSynchronizationSetting{
		Kind: datashare.KindBasicSynchronizationSettingKindScheduleBased,
		ScheduledSynchronizationSettingProperties: &datashare.ScheduledSynchronizationSettingProperties{
			RecurrenceInterval:  datashare.RecurrenceInterval(snapshotSchedule["recurrence"].(string)),
			SynchronizationTime: &date.Time{Time: startTime},
		},
	}
}

func flattenAzureRmDataShareSnapshotSchedule(sync *datashare.ScheduledSynchronizationSetting) []interface{} {
	if sync == nil {
		return []interface{}{}
	}

	var startTime string
	if sync.SynchronizationTime != nil && !sync.SynchronizationTime.IsZero() {
		startTime = sync.SynchronizationTime.Format(time.RFC3339)
	}

	return []interface{}{
		map[string]interface{}{
			"name":       sync.Name,
			"recurrence": string(sync.RecurrenceInterval),
			"start_time": startTime,
		},
	}
}
