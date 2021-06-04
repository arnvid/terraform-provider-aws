package aws

import (
	"fmt"
	"log"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/awserr"
	"github.com/aws/aws-sdk-go/service/s3"
	"github.com/hashicorp/aws-sdk-go-base/tfawserr"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/resource"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/validation"
	awsprovider "github.com/terraform-providers/terraform-provider-aws/provider"
)

func resourceAwsS3BucketPolicy() *schema.Resource {
	return &schema.Resource{
		Create: resourceAwsS3BucketPolicyPut,
		Read:   resourceAwsS3BucketPolicyRead,
		Update: resourceAwsS3BucketPolicyPut,
		Delete: resourceAwsS3BucketPolicyDelete,
		Importer: &schema.ResourceImporter{
			State: schema.ImportStatePassthrough,
		},

		Schema: map[string]*schema.Schema{
			"bucket": {
				Type:     schema.TypeString,
				Required: true,
				ForceNew: true,
			},

			"policy": {
				Type:             schema.TypeString,
				Required:         true,
				ValidateFunc:     validation.StringIsJSON,
				DiffSuppressFunc: suppressEquivalentAwsPolicyDiffs,
			},
		},
	}
}

func resourceAwsS3BucketPolicyPut(d *schema.ResourceData, meta interface{}) error {
	S3Conn := meta.(*awsprovider.AWSClient).S3Conn

	bucket := d.Get("bucket").(string)
	policy := d.Get("policy").(string)

	log.Printf("[DEBUG] S3 bucket: %s, put policy: %s", bucket, policy)

	params := &s3.PutBucketPolicyInput{
		Bucket: aws.String(bucket),
		Policy: aws.String(policy),
	}

	err := resource.Retry(1*time.Minute, func() *resource.RetryError {
		_, err := S3Conn.PutBucketPolicy(params)
		if tfawserr.ErrMessageContains(err, "MalformedPolicy", "") {
			return resource.RetryableError(err)
		}
		if err != nil {
			return resource.NonRetryableError(err)
		}
		return nil
	})
	if isResourceTimeoutError(err) {
		_, err = S3Conn.PutBucketPolicy(params)
	}
	if err != nil {
		return fmt.Errorf("Error putting S3 policy: %s", err)
	}

	d.SetId(bucket)

	return nil
}

func resourceAwsS3BucketPolicyRead(d *schema.ResourceData, meta interface{}) error {
	S3Conn := meta.(*awsprovider.AWSClient).S3Conn

	log.Printf("[DEBUG] S3 bucket policy, read for bucket: %s", d.Id())
	pol, err := S3Conn.GetBucketPolicy(&s3.GetBucketPolicyInput{
		Bucket: aws.String(d.Id()),
	})

	v := ""
	if err == nil && pol.Policy != nil {
		v = *pol.Policy
	}
	if err := d.Set("policy", v); err != nil {
		return err
	}
	if err := d.Set("bucket", d.Id()); err != nil {
		return err
	}

	return nil
}

func resourceAwsS3BucketPolicyDelete(d *schema.ResourceData, meta interface{}) error {
	S3Conn := meta.(*awsprovider.AWSClient).S3Conn

	bucket := d.Get("bucket").(string)

	log.Printf("[DEBUG] S3 bucket: %s, delete policy", bucket)
	_, err := S3Conn.DeleteBucketPolicy(&s3.DeleteBucketPolicyInput{
		Bucket: aws.String(bucket),
	})

	if err != nil {
		if awsErr, ok := err.(awserr.Error); ok && awsErr.Code() == "NoSuchBucket" {
			return nil
		}
		return fmt.Errorf("Error deleting S3 policy: %s", err)
	}

	return nil
}
