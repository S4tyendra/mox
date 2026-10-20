package imapserver

import (
	"testing"
	"time"

	"github.com/mjl-/mox/imapclient"
)

func TestSort(t *testing.T) {
	tc := start(t, false)
	defer tc.close()
	tc.login("mjl@mox.example", password0)
	tc.client.Select("inbox")

	early := time.Date(2020, time.January, 1, 10, 0, 0, 0, time.UTC)
	late := time.Date(2022, time.January, 1, 10, 0, 0, 0, time.UTC)
	tc.client.Append("inbox", makeAppendTime(exampleMsg, late))
	tc.client.Append("inbox", makeAppendTime(searchMsg, early))

	tc.transactf("ok", "uid sort (arrival) utf-8 all")
	tc.xuntagged(imapclient.UntaggedSort{2, 1})

	tc.transactf("ok", "uid sort (reverse arrival) utf-8 all")
	tc.xuntagged(imapclient.UntaggedSort{1, 2})

	tc.transactf("ok", "sort (size) utf-8 all")
}
