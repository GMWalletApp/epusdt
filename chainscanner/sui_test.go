package scanner

import (
	pb "github.com/block-vision/sui-go-sdk/pb/sui/rpc/v2"
	"github.com/tidwall/gjson"
	"google.golang.org/protobuf/proto"
	"slices"
	"testing"
)

func TestSuiOwnerAndSponsorActivity(t *testing.T) {
	tx := &pb.ExecutedTransaction{Digest: proto.String("tx"), Transaction: &pb.Transaction{Sender: proto.String("0xa"), GasPayment: &pb.GasPayment{Owner: proto.String("0xb")}}, Effects: &pb.TransactionEffects{Status: &pb.ExecutionStatus{Success: proto.Bool(false)}, ChangedObjects: []*pb.ChangedObject{{ObjectId: proto.String("0x2"), OutputOwner: &pb.Owner{Address: proto.String("0xc")}}}}, BalanceChanges: []*pb.BalanceChange{{Address: proto.String("0xd"), CoinType: proto.String("0x2::sui::SUI"), Amount: proto.String("1000")}}}
	e, err := suiActivity(tx, 18, 100)
	if err != nil {
		t.Fatal(err)
	}
	for _, addr := range []string{"0xa", "0xb", "0xc", "0xd", "0x2"} {
		a, _ := Canonical("sui", addr)
		if !slices.Contains(e.Addresses, a) {
			t.Fatal("missing", addr)
		}
	}
	if status := gjson.GetBytes(e.Data, "effects.status.success"); status.Raw != "false" {
		t.Fatal("failed transaction evidence", status)
	}
}
