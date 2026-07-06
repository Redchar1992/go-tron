package actuator

import (
	core "github.com/Redchar1992/go-tron/internal/proto/core"
	"github.com/Redchar1992/go-tron/internal/state"
	"github.com/Redchar1992/go-tron/internal/tvm"
)

// accountPermissions resolves account multisig permissions from the node account store for
// the validatemultisign precompile (tvm 0x0a). It is the state-facing adapter for
// tvm.AccountPermissionReader, reproducing AccountCapsule.getPermissionById /
// TransactionCapsule.getWeight over stored core.Account state.
type accountPermissions struct{ st *state.State }

// PermissionById returns the account's permission by id as (threshold, weighted keys), or
// ok=false when the account or the permission does not exist.
func (p accountPermissions) PermissionById(addr []byte, id int) (int64, []tvm.PermissionKey, bool) {
	a, err := p.st.Accounts.Get(addr)
	if err != nil || a == nil {
		return 0, nil, false
	}
	perm := permissionByID(a, id)
	if perm == nil {
		return 0, nil, false
	}
	keys := make([]tvm.PermissionKey, 0, len(perm.GetKeys()))
	for _, k := range perm.GetKeys() {
		keys = append(keys, tvm.PermissionKey{Address: k.GetAddress(), Weight: k.GetWeight()})
	}
	return perm.GetThreshold(), keys, true
}

// permissionByID mirrors AccountCapsule.getPermissionById: id 0 is the owner permission
// (falling back to the implicit default when none is set), id 1 the witness permission (nil
// when unset), and any other id an entry in the active-permission list.
func permissionByID(a *core.Account, id int) *core.Permission {
	switch id {
	case 0:
		if a.GetOwnerPermission() != nil {
			return a.GetOwnerPermission()
		}
		return defaultOwnerPermission(a.GetAddress())
	case 1:
		return a.GetWitnessPermission()
	default:
		for _, perm := range a.GetActivePermission() {
			if int(perm.GetId()) == id {
				return perm
			}
		}
		return nil
	}
}

// defaultOwnerPermission mirrors AccountCapsule.createDefaultOwnerPermission: a single-key
// permission (the account itself, weight 1, threshold 1) used when an account has set no
// explicit owner permission.
func defaultOwnerPermission(addr []byte) *core.Permission {
	return &core.Permission{
		Type:           core.Permission_Owner,
		Id:             0,
		PermissionName: "owner",
		Threshold:      1,
		ParentId:       0,
		Keys:           []*core.Key{{Address: addr, Weight: 1}},
	}
}
