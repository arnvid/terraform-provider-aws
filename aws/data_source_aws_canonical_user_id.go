package aws

import (
	"fmt"
	"log"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/s3"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
	awsprovider "github.com/terraform-providers/terraform-provider-aws/provider"
)

func dataSourceAwsCanonicalUserId() *schema.Resource {
	return &schema.Resource{
		Read: dataSourceAwsCanonicalUserIdRead,

		Schema: map[string]*schema.Schema{
			"display_name": {
				Type:     schema.TypeString,
				Computed: true,
			},
		},
	}
}

func dataSourceAwsCanonicalUserIdRead(d *schema.ResourceData, meta interface{}) error {
	conn := meta.(*awsprovider.AWSClient).S3Conn

	log.Printf("[DEBUG] Reading S3 Buckets")

	req := &s3.ListBucketsInput{}
	resp, err := conn.ListBuckets(req)
	if err != nil {
		return err
	}
	if resp == nil || resp.Owner == nil {
		return fmt.Errorf("no canonical user ID found")
	}

	d.SetId(aws.StringValue(resp.Owner.ID))
	d.Set("display_name", resp.Owner.DisplayName)

	return nil
}
