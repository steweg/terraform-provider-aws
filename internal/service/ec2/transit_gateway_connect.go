package ec2

import (
	"fmt"
	"log"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/hashicorp/aws-sdk-go-base/tfawserr"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/resource"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/validation"
	"github.com/hashicorp/terraform-provider-aws/internal/conns"
	tftags "github.com/hashicorp/terraform-provider-aws/internal/tags"
	"github.com/hashicorp/terraform-provider-aws/internal/tfresource"
	"github.com/hashicorp/terraform-provider-aws/internal/verify"
)

func ResourceTransitGatewayConnect() *schema.Resource {
	return &schema.Resource{
		Create: resourceTransitGatewayConnectCreate,
		Read:   resourceTransitGatewayConnectRead,
		Update: resourceTransitGatewayConnectUpdate,
		Delete: resourceTransitGatewayConnectDelete,
		Importer: &schema.ResourceImporter{
			State: schema.ImportStatePassthrough,
		},

		CustomizeDiff: verify.SetTagsDiff,

		Schema: map[string]*schema.Schema{
			"tags":     tftags.TagsSchema(),
			"tags_all": tftags.TagsSchemaComputed(),
			"transit_gateway_default_route_table_association": {
				Type:     schema.TypeBool,
				Optional: true,
				Default:  true,
			},
			"transit_gateway_default_route_table_propagation": {
				Type:     schema.TypeBool,
				Optional: true,
				Default:  true,
			},
			"transit_gateway_id": {
				Type:         schema.TypeString,
				Required:     true,
				ForceNew:     true,
				ValidateFunc: validation.NoZeroValues,
			},
			"transport_attachment_id": {
				Type:         schema.TypeString,
				Required:     true,
				ForceNew:     true,
				ValidateFunc: validation.NoZeroValues,
			},
		},
	}
}

func resourceTransitGatewayConnectCreate(d *schema.ResourceData, meta interface{}) error {
	conn := meta.(*conns.AWSClient).EC2Conn
	defaultTagsConfig := meta.(*conns.AWSClient).DefaultTagsConfig
	tags := defaultTagsConfig.MergeTags(tftags.New(d.Get("tags").(map[string]interface{})))

	transportAttachmentID := d.Get("transport_attachment_id").(string)

	input := &ec2.CreateTransitGatewayConnectInput{
		Options: &ec2.CreateTransitGatewayConnectRequestOptions{
			Protocol: aws.String("gre"),
		},
		TransportTransitGatewayAttachmentId: aws.String(transportAttachmentID),
		TagSpecifications:                   ec2TagSpecificationsFromKeyValueTags(tags, ec2.ResourceTypeTransitGatewayAttachment),
	}

	log.Printf("[DEBUG] Creating EC2 Transit Gateway Connect Attachment: %s", input)
	output, err := conn.CreateTransitGatewayConnect(input)
	if err != nil {
		return fmt.Errorf("error creating EC2 Transit Gateway Connect Attachment: %s", err)
	}

	d.SetId(aws.StringValue(output.TransitGatewayConnect.TransitGatewayAttachmentId))

	if err := waitForTransitGatewayAttachmentCreation(conn, d.Id()); err != nil {
		return fmt.Errorf("error waiting for EC2 Transit Gateway Connect Attachment (%s) availability: %s", d.Id(), err)
	}

	transportAttachment, err := DescribeTransitGatewayAttachment(conn, transportAttachmentID)
	if err != nil {
		return fmt.Errorf("error describing EC2 Transit Gateway Attachment (%s): %s", transportAttachmentID, err)
	}

	transitGateway, err := DescribeTransitGateway(conn, *transportAttachment.TransitGatewayId)
	if err != nil {
		return fmt.Errorf("error describing EC2 Transit Gateway (%s): %s", *transportAttachment.TransitGatewayId, err)
	}

	if transitGateway.Options == nil {
		return fmt.Errorf("error describing EC2 Transit Gateway (%s): missing options", *transportAttachment.TransitGatewayId)
	}

	// We cannot modify Transit Gateway Route Tables for Resource Access Manager shared Transit Gateways
	if aws.StringValue(transitGateway.OwnerId) == aws.StringValue(transportAttachment.ResourceOwnerId) {
		if err := transitGatewayRouteTableAssociationUpdate(conn, aws.StringValue(transitGateway.Options.AssociationDefaultRouteTableId), d.Id(), d.Get("transit_gateway_default_route_table_association").(bool)); err != nil {
			return fmt.Errorf("error updating EC2 Transit Gateway Attachment (%s) Route Table (%s) association: %s", d.Id(), aws.StringValue(transitGateway.Options.AssociationDefaultRouteTableId), err)
		}

		if err := transitGatewayRouteTablePropagationUpdate(conn, aws.StringValue(transitGateway.Options.PropagationDefaultRouteTableId), d.Id(), d.Get("transit_gateway_default_route_table_propagation").(bool)); err != nil {
			return fmt.Errorf("error updating EC2 Transit Gateway Attachment (%s) Route Table (%s) propagation: %s", d.Id(), aws.StringValue(transitGateway.Options.PropagationDefaultRouteTableId), err)
		}
	}

	return resourceTransitGatewayConnectRead(d, meta)
}

func resourceTransitGatewayConnectRead(d *schema.ResourceData, meta interface{}) error {
	conn := meta.(*conns.AWSClient).EC2Conn
	defaultTagsConfig := meta.(*conns.AWSClient).DefaultTagsConfig
	ignoreTagsConfig := meta.(*conns.AWSClient).IgnoreTagsConfig

	transitGatewayConnect, err := DescribeTransitGatewayConnect(conn, d.Id())

	if tfawserr.ErrMessageContains(err, "InvalidTransitGatewayAttachmentID.NotFound", "") {
		log.Printf("[WARN] EC2 Transit Gateway Connect Attachment (%s) not found, removing from state", d.Id())
		d.SetId("")
		return nil
	}

	if err != nil {
		return fmt.Errorf("error reading EC2 Transit Gateway Connect Attachment: %s", err)
	}

	if transitGatewayConnect == nil {
		log.Printf("[WARN] EC2 Transit Gateway Connect Attachment (%s) not found, removing from state", d.Id())
		d.SetId("")
		return nil
	}

	if aws.StringValue(transitGatewayConnect.State) == ec2.TransitGatewayAttachmentStateDeleting || aws.StringValue(transitGatewayConnect.State) == ec2.TransitGatewayAttachmentStateDeleted {
		log.Printf("[WARN] EC2 Transit Gateway Connect Attachment (%s) in deleted state (%s), removing from state", d.Id(), aws.StringValue(transitGatewayConnect.State))
		d.SetId("")
		return nil
	}

	transitGatewayID := *transitGatewayConnect.TransitGatewayId
	transitGateway, err := DescribeTransitGateway(conn, transitGatewayID)
	if err != nil {
		return fmt.Errorf("error describing EC2 Transit Gateway (%s): %s", transitGatewayID, err)
	}

	transitGatewayAttachment, err := DescribeTransitGatewayAttachment(conn, d.Id())
	if err != nil {
		return fmt.Errorf("error reading EC2 Transit Gateway Attachment: %s", err)
	}

	if transitGatewayAttachment == nil {
		log.Printf("[WARN] EC2 Transit Gateway Attachment (%s) not found, removing from state", d.Id())
		d.SetId("")
		return nil
	}

	// We cannot read Transit Gateway Route Tables for Resource Access Manager shared Transit Gateways
	// Default these to a non-nil value so we can match the existing schema of Default: true
	transitGatewayDefaultRouteTableAssociation := &ec2.TransitGatewayRouteTableAssociation{}
	transitGatewayDefaultRouteTablePropagation := &ec2.TransitGatewayRouteTablePropagation{}
	if aws.StringValue(transitGateway.OwnerId) == aws.StringValue(transitGatewayAttachment.ResourceOwnerId) {
		transitGatewayAssociationDefaultRouteTableID := aws.StringValue(transitGateway.Options.AssociationDefaultRouteTableId)
		transitGatewayDefaultRouteTableAssociation, err = DescribeTransitGatewayRouteTableAssociation(conn, transitGatewayAssociationDefaultRouteTableID, d.Id())
		if err != nil {
			return fmt.Errorf("error determining EC2 Transit Gateway Attachment (%s) association to Route Table (%s): %s", d.Id(), transitGatewayAssociationDefaultRouteTableID, err)
		}

		transitGatewayPropagationDefaultRouteTableID := aws.StringValue(transitGateway.Options.PropagationDefaultRouteTableId)
		transitGatewayDefaultRouteTablePropagation, err = FindTransitGatewayRouteTablePropagation(conn, transitGatewayPropagationDefaultRouteTableID, d.Id())
		if err != nil {
			return fmt.Errorf("error determining EC2 Transit Gateway Attachment (%s) propagation to Route Table (%s): %s", d.Id(), transitGatewayPropagationDefaultRouteTableID, err)
		}
	}

	tags := KeyValueTags(transitGatewayConnect.Tags).IgnoreAWS().IgnoreConfig(ignoreTagsConfig)

	//lintignore:AWSR002
	if err := d.Set("tags", tags.RemoveDefaultConfig(defaultTagsConfig).Map()); err != nil {
		return fmt.Errorf("error setting tags: %w", err)
	}

	if err := d.Set("tags_all", tags.Map()); err != nil {
		return fmt.Errorf("error setting tags_all: %w", err)
	}

	d.Set("transit_gateway_default_route_table_association", (transitGatewayDefaultRouteTableAssociation != nil))
	d.Set("transit_gateway_default_route_table_propagation", (transitGatewayDefaultRouteTablePropagation != nil))
	d.Set("transit_gateway_id", transitGatewayConnect.TransitGatewayId)
	d.Set("transport_attachment_id", transitGatewayConnect.TransportTransitGatewayAttachmentId)

	return nil
}

func resourceTransitGatewayConnectUpdate(d *schema.ResourceData, meta interface{}) error {
	conn := meta.(*conns.AWSClient).EC2Conn

	if d.HasChanges("transit_gateway_default_route_table_association", "transit_gateway_default_route_table_propagation") {
		transitGatewayID := d.Get("transit_gateway_id").(string)

		transitGateway, err := DescribeTransitGateway(conn, transitGatewayID)
		if err != nil {
			return fmt.Errorf("error describing EC2 Transit Gateway (%s): %s", transitGatewayID, err)
		}

		if transitGateway.Options == nil {
			return fmt.Errorf("error describing EC2 Transit Gateway (%s): missing options", transitGatewayID)
		}

		if d.HasChange("transit_gateway_default_route_table_association") {
			if err := transitGatewayRouteTableAssociationUpdate(conn, aws.StringValue(transitGateway.Options.AssociationDefaultRouteTableId), d.Id(), d.Get("transit_gateway_default_route_table_association").(bool)); err != nil {
				return fmt.Errorf("error updating EC2 Transit Gateway Attachment (%s) Route Table (%s) association: %s", d.Id(), aws.StringValue(transitGateway.Options.AssociationDefaultRouteTableId), err)
			}
		}

		if d.HasChange("transit_gateway_default_route_table_propagation") {
			if err := transitGatewayRouteTablePropagationUpdate(conn, aws.StringValue(transitGateway.Options.PropagationDefaultRouteTableId), d.Id(), d.Get("transit_gateway_default_route_table_propagation").(bool)); err != nil {
				return fmt.Errorf("error updating EC2 Transit Gateway Attachment (%s) Route Table (%s) propagation: %s", d.Id(), aws.StringValue(transitGateway.Options.PropagationDefaultRouteTableId), err)
			}
		}
	}

	if d.HasChange("tags_all") {
		o, n := d.GetChange("tags_all")

		if err := UpdateTags(conn, d.Id(), o, n); err != nil {
			return fmt.Errorf("error updating EC2 Transit Gateway Connect Attachment (%s) tags: %s", d.Id(), err)
		}
	}

	return nil
}

func resourceTransitGatewayConnectDelete(d *schema.ResourceData, meta interface{}) error {
	conn := meta.(*conns.AWSClient).EC2Conn

	input := &ec2.DeleteTransitGatewayConnectInput{
		TransitGatewayAttachmentId: aws.String(d.Id()),
	}

	log.Printf("[DEBUG] Deleting EC2 Transit Gateway Connect Attachment (%s): %s", d.Id(), input)
	_, err := conn.DeleteTransitGatewayConnect(input)

	if tfawserr.ErrMessageContains(err, "InvalidTransitGatewayAttachmentID.NotFound", "") {
		return nil
	}

	if err != nil {
		return fmt.Errorf("error deleting EC2 Transit Gateway Connect Attachment: %s", err)
	}

	if err := WaitForTransitGatewayAttachmentDeletion(conn, d.Id()); err != nil {
		return fmt.Errorf("error waiting for EC2 Transit Gateway Connect Attachment (%s) deletion: %s", d.Id(), err)
	}

	return nil
}

func DescribeTransitGatewayConnectPeer(conn *ec2.EC2, transitGatewayConnectPeerID string) (*ec2.TransitGatewayConnectPeer, error) {
	input := &ec2.DescribeTransitGatewayConnectPeersInput{
		TransitGatewayConnectPeerIds: []*string{aws.String(transitGatewayConnectPeerID)},
	}

	log.Printf("[DEBUG] Reading EC2 Transit Gateway Connect Peer (%s): %s", transitGatewayConnectPeerID, input)
	for {
		output, err := conn.DescribeTransitGatewayConnectPeers(input)

		if err != nil {
			return nil, err
		}

		if output == nil || len(output.TransitGatewayConnectPeers) == 0 {
			return nil, nil
		}

		for _, transitGatewayConnectPeer := range output.TransitGatewayConnectPeers {
			if transitGatewayConnectPeer == nil {
				continue
			}

			if aws.StringValue(transitGatewayConnectPeer.TransitGatewayConnectPeerId) == transitGatewayConnectPeerID {
				return transitGatewayConnectPeer, nil
			}
		}

		if aws.StringValue(output.NextToken) == "" {
			break
		}

		input.NextToken = output.NextToken
	}

	return nil, nil
}

func transitGatewayConnectPeerRefreshFunc(conn *ec2.EC2, transitGatewayConnectPeerID string) resource.StateRefreshFunc {
	return func() (interface{}, string, error) {
		transitGatewayConnectPeer, err := DescribeTransitGatewayConnectPeer(conn, transitGatewayConnectPeerID)

		if tfawserr.ErrMessageContains(err, "InvalidTransitGatewayConnectPeerID.NotFound", "") {
			return nil, ec2.TransitGatewayConnectPeerStateDeleted, nil
		}

		if err != nil {
			return nil, "", fmt.Errorf("error reading EC2 Transit Gateway Connect Peer (%s): %s", transitGatewayConnectPeerID, err)
		}

		if transitGatewayConnectPeer == nil {
			return nil, ec2.TransitGatewayConnectPeerStateDeleted, nil
		}

		return transitGatewayConnectPeer, aws.StringValue(transitGatewayConnectPeer.State), nil
	}
}

func waitForTransitGatewayConnectPeerCreation(conn *ec2.EC2, transitGatewayConnectPeerID string) error {
	stateConf := &resource.StateChangeConf{
		Pending: []string{ec2.TransitGatewayConnectPeerStatePending},
		Target:  []string{ec2.TransitGatewayConnectPeerStateAvailable},
		Refresh: transitGatewayConnectPeerRefreshFunc(conn, transitGatewayConnectPeerID),
		Timeout: 10 * time.Minute,
	}

	log.Printf("[DEBUG] Waiting for EC2 Transit Gateway Connect Peer (%s) availability", transitGatewayConnectPeerID)
	_, err := stateConf.WaitForState()

	return err
}

func WaitForTransitGatewayConnectPeerDeletion(conn *ec2.EC2, transitGatewayConnectPeerID string) error {
	stateConf := &resource.StateChangeConf{
		Pending: []string{
			ec2.TransitGatewayConnectPeerStateAvailable,
			ec2.TransitGatewayConnectPeerStateDeleting,
		},
		Target:         []string{ec2.TransitGatewayConnectPeerStateDeleted},
		Refresh:        transitGatewayConnectPeerRefreshFunc(conn, transitGatewayConnectPeerID),
		Timeout:        10 * time.Minute,
		NotFoundChecks: 1,
	}

	log.Printf("[DEBUG] Waiting for EC2 Transit Gateway Connect Peer (%s) deletion", transitGatewayConnectPeerID)
	_, err := stateConf.WaitForState()

	if tfresource.NotFound(err) {
		return nil
	}

	return err
}
