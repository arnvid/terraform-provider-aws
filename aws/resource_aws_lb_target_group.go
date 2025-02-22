package aws

import (
	"context"
	"errors"
	"fmt"
	"log"
	"regexp"
	"strconv"
	"strings"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/elbv2"
	"github.com/hashicorp/aws-sdk-go-base/tfawserr"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/customdiff"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/resource"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/validation"
	"github.com/terraform-providers/terraform-provider-aws/aws/internal/keyvaluetags"
	"github.com/terraform-providers/terraform-provider-aws/aws/internal/service/elbv2/finder"
	"github.com/terraform-providers/terraform-provider-aws/aws/internal/service/elbv2/waiter"
	"github.com/terraform-providers/terraform-provider-aws/aws/internal/tfresource"
)

func resourceAwsLbTargetGroup() *schema.Resource {
	return &schema.Resource{
		// NLBs have restrictions on them at this time
		CustomizeDiff: customdiff.Sequence(
			resourceAwsLbTargetGroupCustomizeDiff,
			SetTagsDiff,
		),

		Create: resourceAwsLbTargetGroupCreate,
		Read:   resourceAwsLbTargetGroupRead,
		Update: resourceAwsLbTargetGroupUpdate,
		Delete: resourceAwsLbTargetGroupDelete,
		Importer: &schema.ResourceImporter{
			State: schema.ImportStatePassthrough,
		},

		Schema: map[string]*schema.Schema{
			"arn": {
				Type:     schema.TypeString,
				Computed: true,
			},
			"arn_suffix": {
				Type:     schema.TypeString,
				Computed: true,
			},
			"deregistration_delay": {
				Type:         schema.TypeInt,
				Optional:     true,
				Default:      300,
				ValidateFunc: validation.IntBetween(0, 3600),
			},
			"health_check": {
				Type:     schema.TypeList,
				Optional: true,
				Computed: true,
				MaxItems: 1,
				Elem: &schema.Resource{
					Schema: map[string]*schema.Schema{
						"enabled": {
							Type:     schema.TypeBool,
							Optional: true,
							Default:  true,
						},
						"healthy_threshold": {
							Type:         schema.TypeInt,
							Optional:     true,
							Default:      3,
							ValidateFunc: validation.IntBetween(2, 10),
						},
						"interval": {
							Type:     schema.TypeInt,
							Optional: true,
							Default:  30,
						},
						"matcher": {
							Type:     schema.TypeString,
							Computed: true,
							Optional: true,
						},
						"path": {
							Type:         schema.TypeString,
							Optional:     true,
							Computed:     true,
							ValidateFunc: validateAwsLbTargetGroupHealthCheckPath,
						},
						"port": {
							Type:             schema.TypeString,
							Optional:         true,
							Default:          "traffic-port",
							ValidateFunc:     validateAwsLbTargetGroupHealthCheckPort,
							DiffSuppressFunc: suppressIfTargetType(elbv2.TargetTypeEnumLambda),
						},
						"protocol": {
							Type:     schema.TypeString,
							Optional: true,
							Default:  elbv2.ProtocolEnumHttp,
							StateFunc: func(v interface{}) string {
								return strings.ToUpper(v.(string))
							},
							ValidateFunc: validation.StringInSlice([]string{
								elbv2.ProtocolEnumHttp,
								elbv2.ProtocolEnumHttps,
								elbv2.ProtocolEnumTcp,
							}, true),
							DiffSuppressFunc: suppressIfTargetType(elbv2.TargetTypeEnumLambda),
						},
						"timeout": {
							Type:         schema.TypeInt,
							Optional:     true,
							Computed:     true,
							ValidateFunc: validation.IntBetween(2, 120),
						},
						"unhealthy_threshold": {
							Type:         schema.TypeInt,
							Optional:     true,
							Default:      3,
							ValidateFunc: validation.IntBetween(2, 10),
						},
					},
				},
			},
			"lambda_multi_value_headers_enabled": {
				Type:     schema.TypeBool,
				Optional: true,
				Default:  false,
			},
			"load_balancing_algorithm_type": {
				Type:     schema.TypeString,
				Optional: true,
				Computed: true,
				ValidateFunc: validation.StringInSlice([]string{
					"round_robin",
					"least_outstanding_requests",
				}, false),
			},
			"name": {
				Type:          schema.TypeString,
				Optional:      true,
				Computed:      true,
				ForceNew:      true,
				ConflictsWith: []string{"name_prefix"},
				ValidateFunc:  validateLbTargetGroupName,
			},
			"name_prefix": {
				Type:          schema.TypeString,
				Optional:      true,
				ForceNew:      true,
				ConflictsWith: []string{"name"},
				ValidateFunc:  validateLbTargetGroupNamePrefix,
			},
			"port": {
				Type:         schema.TypeInt,
				Optional:     true,
				ForceNew:     true,
				ValidateFunc: validation.IntBetween(1, 65535),
			},
			"preserve_client_ip": {
				// Use TypeString to allow an "unspecified" value,
				// since TypeBool only has true/false with false default.
				// The conversion from bare true/false values in
				// configurations to TypeString value is currently safe.
				Type:             schema.TypeString,
				Optional:         true,
				Computed:         true,
				DiffSuppressFunc: suppressEquivalentTypeStringBoolean,
				ValidateFunc:     validateTypeStringNullableBoolean,
			},
			"protocol": {
				Type:         schema.TypeString,
				Optional:     true,
				ForceNew:     true,
				ValidateFunc: validation.StringInSlice(elbv2.ProtocolEnum_Values(), true),
			},
			"protocol_version": {
				Type:     schema.TypeString,
				Optional: true,
				Computed: true,
				StateFunc: func(v interface{}) string {
					return strings.ToUpper(v.(string))
				},
				ValidateFunc: validation.StringInSlice([]string{
					"GRPC",
					"HTTP1",
					"HTTP2",
				}, true),
				DiffSuppressFunc: func(k, old, new string, d *schema.ResourceData) bool {
					if d.Get("target_type").(string) == elbv2.TargetTypeEnumLambda {
						return true
					}
					switch d.Get("protocol").(string) {
					case elbv2.ProtocolEnumHttp, elbv2.ProtocolEnumHttps:
						return false
					}
					return true
				},
			},
			"proxy_protocol_v2": {
				Type:     schema.TypeBool,
				Optional: true,
				Default:  false,
			},
			"slow_start": {
				Type:         schema.TypeInt,
				Optional:     true,
				Default:      0,
				ValidateFunc: validateSlowStart,
			},
			"stickiness": {
				Type:     schema.TypeList,
				Optional: true,
				Computed: true,
				MaxItems: 1,
				Elem: &schema.Resource{
					Schema: map[string]*schema.Schema{
						"cookie_duration": {
							Type:         schema.TypeInt,
							Optional:     true,
							Default:      86400,
							ValidateFunc: validation.IntBetween(0, 604800),
							DiffSuppressFunc: func(k, old, new string, d *schema.ResourceData) bool {
								switch d.Get("protocol").(string) {
								case elbv2.ProtocolEnumTcp, elbv2.ProtocolEnumUdp, elbv2.ProtocolEnumTcpUdp, elbv2.ProtocolEnumTls:
									return true
								}
								return false
							},
						},
						"enabled": {
							Type:     schema.TypeBool,
							Optional: true,
							Default:  true,
						},
						"type": {
							Type:     schema.TypeString,
							Required: true,
							ValidateFunc: validation.StringInSlice([]string{
								"lb_cookie", // Only for ALBs
								"source_ip", // Only for NLBs
							}, false),
							DiffSuppressFunc: func(k, old, new string, d *schema.ResourceData) bool {
								switch d.Get("protocol").(string) {
								case elbv2.ProtocolEnumTcp, elbv2.ProtocolEnumUdp, elbv2.ProtocolEnumTcpUdp, elbv2.ProtocolEnumTls:
									if new == "lb_cookie" && !d.Get("stickiness.0.enabled").(bool) {
										log.Printf("[WARN] invalid configuration, this will fail in a future version: stickiness enabled %v, protocol %s, type %s", d.Get("stickiness.0.enabled").(bool), d.Get("protocol").(string), new)
										return true
									}
								}
								return false
							},
						},
					},
				},
			},
			"target_type": {
				Type:         schema.TypeString,
				Optional:     true,
				Default:      elbv2.TargetTypeEnumInstance,
				ForceNew:     true,
				ValidateFunc: validation.StringInSlice(elbv2.TargetTypeEnum_Values(), false),
			},
			"tags":     tagsSchema(),
			"tags_all": tagsSchemaComputed(),
			"vpc_id": {
				Type:     schema.TypeString,
				Optional: true,
				ForceNew: true,
			},
		},
	}
}

func suppressIfTargetType(t string) schema.SchemaDiffSuppressFunc {
	return func(k string, old string, new string, d *schema.ResourceData) bool {
		return d.Get("target_type").(string) == t
	}
}

func resourceAwsLbTargetGroupCreate(d *schema.ResourceData, meta interface{}) error {
	conn := meta.(*AWSClient).elbv2conn

	var groupName string
	if v, ok := d.GetOk("name"); ok {
		groupName = v.(string)
	} else if v, ok := d.GetOk("name_prefix"); ok {
		groupName = resource.PrefixedUniqueId(v.(string))
	} else {
		groupName = resource.PrefixedUniqueId("tf-")
	}

	params := &elbv2.CreateTargetGroupInput{
		Name:       aws.String(groupName),
		TargetType: aws.String(d.Get("target_type").(string)),
	}

	if d.Get("target_type").(string) != elbv2.TargetTypeEnumLambda {
		if _, ok := d.GetOk("port"); !ok {
			return fmt.Errorf("port should be set when target type is %s", d.Get("target_type").(string))
		}

		if _, ok := d.GetOk("protocol"); !ok {
			return fmt.Errorf("protocol should be set when target type is %s", d.Get("target_type").(string))
		}

		if _, ok := d.GetOk("vpc_id"); !ok {
			return fmt.Errorf("vpc_id should be set when target type is %s", d.Get("target_type").(string))
		}
		params.Port = aws.Int64(int64(d.Get("port").(int)))
		params.Protocol = aws.String(d.Get("protocol").(string))
		switch d.Get("protocol").(string) {
		case elbv2.ProtocolEnumHttp, elbv2.ProtocolEnumHttps:
			params.ProtocolVersion = aws.String(d.Get("protocol_version").(string))
		}
		params.VpcId = aws.String(d.Get("vpc_id").(string))
	}

	if healthChecks := d.Get("health_check").([]interface{}); len(healthChecks) == 1 {
		healthCheck := healthChecks[0].(map[string]interface{})

		params.HealthCheckEnabled = aws.Bool(healthCheck["enabled"].(bool))

		params.HealthCheckIntervalSeconds = aws.Int64(int64(healthCheck["interval"].(int)))

		params.HealthyThresholdCount = aws.Int64(int64(healthCheck["healthy_threshold"].(int)))
		params.UnhealthyThresholdCount = aws.Int64(int64(healthCheck["unhealthy_threshold"].(int)))
		t := healthCheck["timeout"].(int)
		if t != 0 {
			params.HealthCheckTimeoutSeconds = aws.Int64(int64(t))
		}
		healthCheckProtocol := healthCheck["protocol"].(string)

		if healthCheckProtocol != elbv2.ProtocolEnumTcp {
			p := healthCheck["path"].(string)
			if p != "" {
				params.HealthCheckPath = aws.String(p)
			}

			m := healthCheck["matcher"].(string)
			protocolVersion := d.Get("protocol_version").(string)
			if m != "" {
				if protocolVersion == "GRPC" {
					params.Matcher = &elbv2.Matcher{
						GrpcCode: aws.String(m),
					}
				} else {
					params.Matcher = &elbv2.Matcher{
						HttpCode: aws.String(m),
					}
				}
			}
		}
		if d.Get("target_type").(string) != elbv2.TargetTypeEnumLambda {
			params.HealthCheckPort = aws.String(healthCheck["port"].(string))
			params.HealthCheckProtocol = aws.String(healthCheckProtocol)
		}
	}

	resp, err := conn.CreateTargetGroup(params)
	if err != nil {
		return fmt.Errorf("error creating LB Target Group: %w", err)
	}

	if len(resp.TargetGroups) == 0 {
		return errors.New("error creating LB Target Group: no groups returned in response")
	}
	d.SetId(aws.StringValue(resp.TargetGroups[0].TargetGroupArn))
	return resourceAwsLbTargetGroupUpdate(d, meta)
}

func resourceAwsLbTargetGroupRead(d *schema.ResourceData, meta interface{}) error {
	conn := meta.(*AWSClient).elbv2conn

	var targetGroup *elbv2.TargetGroup

	err := resource.Retry(waiter.PropagationTimeout, func() *resource.RetryError {
		var err error

		targetGroup, err = finder.TargetGroupByARN(conn, d.Id())

		if d.IsNewResource() && tfawserr.ErrCodeEquals(err, elbv2.ErrCodeTargetGroupNotFoundException) {
			return resource.RetryableError(err)
		}

		if err != nil {
			return resource.NonRetryableError(err)
		}

		if d.IsNewResource() && targetGroup == nil {
			return resource.RetryableError(&resource.NotFoundError{
				LastError: fmt.Errorf("ELBv2 Target Group (%s) not found", d.Id()),
			})
		}

		return nil
	})

	if tfresource.TimedOut(err) {
		targetGroup, err = finder.TargetGroupByARN(conn, d.Id())
	}

	if !d.IsNewResource() && tfawserr.ErrCodeEquals(err, elbv2.ErrCodeTargetGroupNotFoundException) {
		log.Printf("[WARN] ELBv2 Target Group (%s) not found, removing from state", d.Id())
		d.SetId("")
		return nil
	}

	if err != nil {
		return fmt.Errorf("error reading ELBv2 Target Group (%s): %w", d.Id(), err)
	}

	if targetGroup == nil {
		if d.IsNewResource() {
			return fmt.Errorf("error reading ELBv2 Target Group (%s): not found after creation", d.Id())
		}

		log.Printf("[WARN] ELBv2 Target Group (%s) not found, removing from state", d.Id())
		d.SetId("")
		return nil
	}

	return flattenAwsLbTargetGroupResource(d, meta, targetGroup)
}

func resourceAwsLbTargetGroupUpdate(d *schema.ResourceData, meta interface{}) error {
	conn := meta.(*AWSClient).elbv2conn

	if d.HasChange("tags_all") {
		o, n := d.GetChange("tags_all")

		err := resource.Retry(waiter.LoadBalancerTagPropagationTimeout, func() *resource.RetryError {
			err := keyvaluetags.Elbv2UpdateTags(conn, d.Id(), o, n)

			if tfawserr.ErrCodeEquals(err, elbv2.ErrCodeTargetGroupNotFoundException) {
				log.Printf("[DEBUG] Retrying tagging of LB (%s)", d.Id())
				return resource.RetryableError(err)
			}

			if err != nil {
				return resource.NonRetryableError(err)
			}

			return nil
		})

		if tfresource.TimedOut(err) {
			err = keyvaluetags.Elbv2UpdateTags(conn, d.Id(), o, n)
		}

		if err != nil {
			return fmt.Errorf("error updating LB Target Group (%s) tags: %w", d.Id(), err)
		}
	}

	if d.HasChange("health_check") {
		var params *elbv2.ModifyTargetGroupInput
		healthChecks := d.Get("health_check").([]interface{})
		if len(healthChecks) == 1 {
			healthCheck := healthChecks[0].(map[string]interface{})

			params = &elbv2.ModifyTargetGroupInput{
				TargetGroupArn:          aws.String(d.Id()),
				HealthCheckEnabled:      aws.Bool(healthCheck["enabled"].(bool)),
				HealthyThresholdCount:   aws.Int64(int64(healthCheck["healthy_threshold"].(int))),
				UnhealthyThresholdCount: aws.Int64(int64(healthCheck["unhealthy_threshold"].(int))),
			}

			t := healthCheck["timeout"].(int)
			if t != 0 {
				params.HealthCheckTimeoutSeconds = aws.Int64(int64(t))
			}

			healthCheckProtocol := healthCheck["protocol"].(string)
			protocolVersion := d.Get("protocol_version").(string)
			if healthCheckProtocol != elbv2.ProtocolEnumTcp && !d.IsNewResource() {
				if protocolVersion == "GRPC" {
					params.Matcher = &elbv2.Matcher{
						GrpcCode: aws.String(healthCheck["matcher"].(string)),
					}
				} else {
					params.Matcher = &elbv2.Matcher{
						HttpCode: aws.String(healthCheck["matcher"].(string)),
					}
				}
				params.HealthCheckPath = aws.String(healthCheck["path"].(string))
				params.HealthCheckIntervalSeconds = aws.Int64(int64(healthCheck["interval"].(int)))
			}
			if d.Get("target_type").(string) != elbv2.TargetTypeEnumLambda {
				params.HealthCheckPort = aws.String(healthCheck["port"].(string))
				params.HealthCheckProtocol = aws.String(healthCheckProtocol)
			}
		}

		if params != nil {
			_, err := conn.ModifyTargetGroup(params)
			if err != nil {
				return fmt.Errorf("error modifying Target Group: %w", err)
			}
		}
	}

	var attrs []*elbv2.TargetGroupAttribute

	switch d.Get("target_type").(string) {
	case elbv2.TargetTypeEnumInstance, elbv2.TargetTypeEnumIp:
		if d.HasChange("deregistration_delay") {
			attrs = append(attrs, &elbv2.TargetGroupAttribute{
				Key:   aws.String("deregistration_delay.timeout_seconds"),
				Value: aws.String(fmt.Sprintf("%d", d.Get("deregistration_delay").(int))),
			})
		}

		if d.HasChange("slow_start") {
			attrs = append(attrs, &elbv2.TargetGroupAttribute{
				Key:   aws.String("slow_start.duration_seconds"),
				Value: aws.String(fmt.Sprintf("%d", d.Get("slow_start").(int))),
			})
		}

		if d.HasChange("proxy_protocol_v2") {
			attrs = append(attrs, &elbv2.TargetGroupAttribute{
				Key:   aws.String("proxy_protocol_v2.enabled"),
				Value: aws.String(strconv.FormatBool(d.Get("proxy_protocol_v2").(bool))),
			})
		}

		if d.HasChange("preserve_client_ip") {
			attrs = append(attrs, &elbv2.TargetGroupAttribute{
				Key:   aws.String("preserve_client_ip.enabled"),
				Value: aws.String(d.Get("preserve_client_ip").(string)),
			})
		}

		if d.HasChange("stickiness") {
			stickinessBlocks := d.Get("stickiness").([]interface{})
			if len(stickinessBlocks) == 1 {
				stickiness := stickinessBlocks[0].(map[string]interface{})

				if !stickiness["enabled"].(bool) && stickiness["type"].(string) == "lb_cookie" && d.Get("protocol").(string) != elbv2.ProtocolEnumHttp && d.Get("protocol").(string) != elbv2.ProtocolEnumHttps {
					log.Printf("[WARN] invalid configuration, this will fail in a future version: stickiness enabled %v, protocol %s, type %s", stickiness["enabled"].(bool), d.Get("protocol").(string), stickiness["type"].(string))
				} else {
					attrs = append(attrs,
						&elbv2.TargetGroupAttribute{
							Key:   aws.String("stickiness.enabled"),
							Value: aws.String(strconv.FormatBool(stickiness["enabled"].(bool))),
						},
						&elbv2.TargetGroupAttribute{
							Key:   aws.String("stickiness.type"),
							Value: aws.String(stickiness["type"].(string)),
						})

					switch d.Get("protocol").(string) {
					case elbv2.ProtocolEnumHttp, elbv2.ProtocolEnumHttps:
						attrs = append(attrs,
							&elbv2.TargetGroupAttribute{
								Key:   aws.String("stickiness.lb_cookie.duration_seconds"),
								Value: aws.String(fmt.Sprintf("%d", stickiness["cookie_duration"].(int))),
							})
					}
				}
			} else if len(stickinessBlocks) == 0 {
				attrs = append(attrs, &elbv2.TargetGroupAttribute{
					Key:   aws.String("stickiness.enabled"),
					Value: aws.String("false"),
				})
			}
		}

		if d.HasChange("load_balancing_algorithm_type") {
			attrs = append(attrs, &elbv2.TargetGroupAttribute{
				Key:   aws.String("load_balancing.algorithm.type"),
				Value: aws.String(d.Get("load_balancing_algorithm_type").(string)),
			})
		}
	case elbv2.TargetTypeEnumLambda:
		if d.HasChange("lambda_multi_value_headers_enabled") {
			attrs = append(attrs, &elbv2.TargetGroupAttribute{
				Key:   aws.String("lambda.multi_value_headers.enabled"),
				Value: aws.String(strconv.FormatBool(d.Get("lambda_multi_value_headers_enabled").(bool))),
			})
		}
	}

	if len(attrs) > 0 {
		params := &elbv2.ModifyTargetGroupAttributesInput{
			TargetGroupArn: aws.String(d.Id()),
			Attributes:     attrs,
		}

		_, err := conn.ModifyTargetGroupAttributes(params)
		if err != nil {
			return fmt.Errorf("error modifying Target Group Attributes: %w", err)
		}
	}

	return resourceAwsLbTargetGroupRead(d, meta)
}

func resourceAwsLbTargetGroupDelete(d *schema.ResourceData, meta interface{}) error {
	conn := meta.(*AWSClient).elbv2conn

	input := &elbv2.DeleteTargetGroupInput{
		TargetGroupArn: aws.String(d.Id()),
	}

	log.Printf("[DEBUG] Deleting Target Group (%s): %s", d.Id(), input)
	err := resource.Retry(waiter.TargetGroupDeleteTimeout, func() *resource.RetryError {
		_, err := conn.DeleteTargetGroup(input)

		if tfawserr.ErrMessageContains(err, "ResourceInUse", "is currently in use by a listener or a rule") {
			return resource.RetryableError(err)
		}

		if err != nil {
			return resource.NonRetryableError(err)
		}

		return nil
	})

	if tfresource.TimedOut(err) {
		_, err = conn.DeleteTargetGroup(input)
	}

	if err != nil {
		return fmt.Errorf("error deleting Target Group: %w", err)
	}

	return nil
}

func validateAwsLbTargetGroupHealthCheckPath(v interface{}, k string) (ws []string, errors []error) {
	value := v.(string)
	if len(value) > 1024 {
		errors = append(errors, fmt.Errorf(
			"%q cannot be longer than 1024 characters: %q", k, value))
	}
	if len(value) > 0 && !strings.HasPrefix(value, "/") {
		errors = append(errors, fmt.Errorf(
			"%q must begin with a '/' character: %q", k, value))
	}
	return
}

func validateSlowStart(v interface{}, k string) (ws []string, errors []error) {
	value := v.(int)

	// Check if the value is between 30-900 or 0 (seconds).
	if value != 0 && !(value >= 30 && value <= 900) {
		errors = append(errors, fmt.Errorf(
			"%q contains an invalid Slow Start Duration \"%d\". "+
				"Valid intervals are 30-900 or 0 to disable.",
			k, value))
	}
	return
}

func validateAwsLbTargetGroupHealthCheckPort(v interface{}, k string) (ws []string, errors []error) {
	value := v.(string)

	if value == "traffic-port" {
		return
	}

	port, err := strconv.Atoi(value)
	if err != nil {
		errors = append(errors, fmt.Errorf("%q must be a valid port number (1-65536) or %q", k, "traffic-port"))
	}

	if port < 1 || port > 65536 {
		errors = append(errors, fmt.Errorf("%q must be a valid port number (1-65536) or %q", k, "traffic-port"))
	}

	return
}

func lbTargetGroupSuffixFromARN(arn *string) string {
	if arn == nil {
		return ""
	}

	if arnComponents := regexp.MustCompile(`arn:.*:targetgroup/(.*)`).FindAllStringSubmatch(*arn, -1); len(arnComponents) == 1 {
		if len(arnComponents[0]) == 2 {
			return fmt.Sprintf("targetgroup/%s", arnComponents[0][1])
		}
	}

	return ""
}

// flattenAwsLbTargetGroupResource takes a *elbv2.TargetGroup and populates all respective resource fields.
func flattenAwsLbTargetGroupResource(d *schema.ResourceData, meta interface{}, targetGroup *elbv2.TargetGroup) error {
	conn := meta.(*AWSClient).elbv2conn
	defaultTagsConfig := meta.(*AWSClient).DefaultTagsConfig
	ignoreTagsConfig := meta.(*AWSClient).IgnoreTagsConfig

	d.Set("arn", targetGroup.TargetGroupArn)
	d.Set("arn_suffix", lbTargetGroupSuffixFromARN(targetGroup.TargetGroupArn))
	d.Set("name", targetGroup.TargetGroupName)
	d.Set("target_type", targetGroup.TargetType)

	if err := d.Set("health_check", flattenLbTargetGroupHealthCheck(targetGroup)); err != nil {
		return fmt.Errorf("error setting health_check: %w", err)
	}

	if v, _ := d.Get("target_type").(string); v != elbv2.TargetTypeEnumLambda {
		d.Set("vpc_id", targetGroup.VpcId)
		d.Set("port", targetGroup.Port)
		d.Set("protocol", targetGroup.Protocol)
	}

	switch d.Get("protocol").(string) {
	case elbv2.ProtocolEnumHttp, elbv2.ProtocolEnumHttps:
		d.Set("protocol_version", targetGroup.ProtocolVersion)
	}

	attrResp, err := conn.DescribeTargetGroupAttributes(&elbv2.DescribeTargetGroupAttributesInput{
		TargetGroupArn: aws.String(d.Id()),
	})
	if err != nil {
		return fmt.Errorf("error retrieving Target Group Attributes: %w", err)
	}

	for _, attr := range attrResp.Attributes {
		switch aws.StringValue(attr.Key) {
		case "deregistration_delay.timeout_seconds":
			timeout, err := strconv.Atoi(aws.StringValue(attr.Value))
			if err != nil {
				return fmt.Errorf("error converting deregistration_delay.timeout_seconds to int: %s", aws.StringValue(attr.Value))
			}
			d.Set("deregistration_delay", timeout)
		case "lambda.multi_value_headers.enabled":
			enabled, err := strconv.ParseBool(aws.StringValue(attr.Value))
			if err != nil {
				return fmt.Errorf("error converting lambda.multi_value_headers.enabled to bool: %s", aws.StringValue(attr.Value))
			}
			d.Set("lambda_multi_value_headers_enabled", enabled)
		case "proxy_protocol_v2.enabled":
			enabled, err := strconv.ParseBool(aws.StringValue(attr.Value))
			if err != nil {
				return fmt.Errorf("error converting proxy_protocol_v2.enabled to bool: %s", aws.StringValue(attr.Value))
			}
			d.Set("proxy_protocol_v2", enabled)
		case "slow_start.duration_seconds":
			slowStart, err := strconv.Atoi(aws.StringValue(attr.Value))
			if err != nil {
				return fmt.Errorf("error converting slow_start.duration_seconds to int: %s", aws.StringValue(attr.Value))
			}
			d.Set("slow_start", slowStart)
		case "load_balancing.algorithm.type":
			loadBalancingAlgorithm := aws.StringValue(attr.Value)
			d.Set("load_balancing_algorithm_type", loadBalancingAlgorithm)
		case "preserve_client_ip.enabled":
			_, err := strconv.ParseBool(aws.StringValue(attr.Value))
			if err != nil {
				return fmt.Errorf("error converting preserve_client_ip.enabled to bool: %s", aws.StringValue(attr.Value))
			}
			d.Set("preserve_client_ip", attr.Value)
		}
	}

	stickinessAttr, err := flattenAwsLbTargetGroupStickiness(attrResp.Attributes)
	if err != nil {
		return fmt.Errorf("error flattening stickiness: %w", err)
	}

	if err := d.Set("stickiness", stickinessAttr); err != nil {
		return fmt.Errorf("error setting stickiness: %w", err)
	}

	tags, err := keyvaluetags.Elbv2ListTags(conn, d.Id())

	if err != nil {
		return fmt.Errorf("error listing tags for LB Target Group (%s): %w", d.Id(), err)
	}

	tags = tags.IgnoreAws().IgnoreConfig(ignoreTagsConfig)

	//lintignore:AWSR002
	if err := d.Set("tags", tags.RemoveDefaultConfig(defaultTagsConfig).Map()); err != nil {
		return fmt.Errorf("error setting tags: %w", err)
	}

	if err := d.Set("tags_all", tags.Map()); err != nil {
		return fmt.Errorf("error setting tags_all: %w", err)
	}

	return nil
}

func flattenAwsLbTargetGroupStickiness(attributes []*elbv2.TargetGroupAttribute) ([]interface{}, error) {
	if len(attributes) == 0 {
		return []interface{}{}, nil
	}

	m := make(map[string]interface{})

	for _, attr := range attributes {
		switch aws.StringValue(attr.Key) {
		case "stickiness.enabled":
			enabled, err := strconv.ParseBool(aws.StringValue(attr.Value))
			if err != nil {
				return nil, fmt.Errorf("error converting stickiness.enabled to bool: %s", aws.StringValue(attr.Value))
			}
			m["enabled"] = enabled
		case "stickiness.type":
			m["type"] = aws.StringValue(attr.Value)
		case "stickiness.lb_cookie.duration_seconds":
			duration, err := strconv.Atoi(aws.StringValue(attr.Value))
			if err != nil {
				return nil, fmt.Errorf("error converting stickiness.lb_cookie.duration_seconds to int: %s", aws.StringValue(attr.Value))
			}
			m["cookie_duration"] = duration
		}
	}

	return []interface{}{m}, nil
}

func resourceAwsLbTargetGroupCustomizeDiff(_ context.Context, diff *schema.ResourceDiff, v interface{}) error {
	protocol := diff.Get("protocol").(string)

	// Network Load Balancers have many special quirks to them.
	// See http://docs.aws.amazon.com/elasticloadbalancing/latest/APIReference/API_CreateTargetGroup.html
	if healthChecks := diff.Get("health_check").([]interface{}); len(healthChecks) == 1 {
		healthCheck := healthChecks[0].(map[string]interface{})
		protocol := healthCheck["protocol"].(string)

		if protocol == elbv2.ProtocolEnumTcp {
			// Cannot set custom matcher on TCP health checks
			if m := healthCheck["matcher"].(string); m != "" {
				return fmt.Errorf("%s: health_check.matcher is not supported for target_groups with TCP protocol", diff.Id())
			}
			// Cannot set custom path on TCP health checks
			if m := healthCheck["path"].(string); m != "" {
				return fmt.Errorf("%s: health_check.path is not supported for target_groups with TCP protocol", diff.Id())
			}
			// Cannot set custom timeout on TCP health checks
			if t := healthCheck["timeout"].(int); t != 0 && diff.Id() == "" {
				// timeout has a default value, so only check this if this is a network
				// LB and is a first run
				return fmt.Errorf("%s: health_check.timeout is not supported for target_groups with TCP protocol", diff.Id())
			}
			if healthCheck["healthy_threshold"].(int) != healthCheck["unhealthy_threshold"].(int) {
				return fmt.Errorf("%s: health_check.healthy_threshold %d and health_check.unhealthy_threshold %d must be the same for target_groups with TCP protocol", diff.Id(), healthCheck["healthy_threshold"].(int), healthCheck["unhealthy_threshold"].(int))
			}
		}
	}

	if strings.Contains(protocol, elbv2.ProtocolEnumHttp) {
		if healthChecks := diff.Get("health_check").([]interface{}); len(healthChecks) == 1 {
			healthCheck := healthChecks[0].(map[string]interface{})
			// HTTP(S) Target Groups cannot use TCP health checks
			if p := healthCheck["protocol"].(string); strings.ToLower(p) == "tcp" {
				return fmt.Errorf("HTTP Target Groups cannot use TCP health checks")
			}
		}
	}

	if diff.Id() == "" {
		return nil
	}

	if protocol == elbv2.ProtocolEnumTcp {
		if diff.HasChange("health_check.0.interval") {
			if err := diff.ForceNew("health_check.0.interval"); err != nil {
				return err
			}
		}
		// The health_check configuration block protocol argument has Default: HTTP, however the block
		// itself is Computed: true. When not configured, a TLS (Network LB) Target Group will default
		// to health check protocol TLS. We do not want to trigger recreation in this scenario.
		// ResourceDiff will show 0 changed keys for the configuration block, which we can use to ensure
		// there was an actual change to trigger the ForceNew.
		if diff.HasChange("health_check.0.protocol") && len(diff.GetChangedKeysPrefix("health_check.0")) != 0 {
			if err := diff.ForceNew("health_check.0.protocol"); err != nil {
				return err
			}
		}
		if diff.HasChange("health_check.0.timeout") {
			if err := diff.ForceNew("health_check.0.timeout"); err != nil {
				return err
			}
		}
	}
	return nil
}

func flattenLbTargetGroupHealthCheck(targetGroup *elbv2.TargetGroup) []interface{} {
	if targetGroup == nil {
		return []interface{}{}
	}

	m := map[string]interface{}{
		"enabled":             aws.BoolValue(targetGroup.HealthCheckEnabled),
		"healthy_threshold":   int(aws.Int64Value(targetGroup.HealthyThresholdCount)),
		"interval":            int(aws.Int64Value(targetGroup.HealthCheckIntervalSeconds)),
		"port":                aws.StringValue(targetGroup.HealthCheckPort),
		"protocol":            aws.StringValue(targetGroup.HealthCheckProtocol),
		"timeout":             int(aws.Int64Value(targetGroup.HealthCheckTimeoutSeconds)),
		"unhealthy_threshold": int(aws.Int64Value(targetGroup.UnhealthyThresholdCount)),
	}

	if targetGroup.HealthCheckPath != nil {
		m["path"] = aws.StringValue(targetGroup.HealthCheckPath)
	}
	if targetGroup.Matcher != nil && targetGroup.Matcher.HttpCode != nil {
		m["matcher"] = aws.StringValue(targetGroup.Matcher.HttpCode)
	}
	if targetGroup.Matcher != nil && targetGroup.Matcher.GrpcCode != nil {
		m["matcher"] = aws.StringValue(targetGroup.Matcher.GrpcCode)
	}

	return []interface{}{m}
}
