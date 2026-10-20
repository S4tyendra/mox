package store

import (
	"testing"

	"github.com/mjl-/bstore"
)

func TestAddressBookAndHideAlias(t *testing.T) {
	acc, err := OpenAccount(pkglog, "mjl", false)
	if err != nil {
		t.Skip(err)
	}
	defer acc.Close()
	err = acc.DB.Write(t.Context(), func(tx *bstore.Tx) error {
		if err := AddressBookUpsert(tx, "Ada@Example.ORG", "Ada"); err != nil {
			return err
		}
		if err := AddressBookUpsert(tx, "ada@example.org", ""); err != nil {
			return err
		}
		list, err := AddressBookSuggest(tx, "ada", 10)
		if err != nil {
			return err
		}
		if len(list) != 1 || list[0].Name != "Ada" || list[0].Uses != 2 {
			t.Fatalf("%#v", list)
		}
		disabled, err := HideAliasDisabled(tx, "you+x@example.org")
		if err != nil || disabled {
			t.Fatalf("absent alias %v %v", disabled, err)
		}
		a := HideAlias{Address: "you+x@example.org", Localpart: "you+x", Domain: "example.org", Enabled: false}
		if err := tx.Insert(&a); err != nil {
			return err
		}
		disabled, err = HideAliasDisabled(tx, "YOU+X@example.org")
		if err != nil || !disabled {
			t.Fatalf("disabled alias %v %v", disabled, err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
