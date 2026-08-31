package transport_test

import (
	"testing"

	"github.com/nexssp/transport"
	"github.com/nexssp/transport/cron"
	"github.com/nexssp/transport/tbus"
	"github.com/nexssp/transport/tcli"
	"github.com/nexssp/transport/thttp"
	"github.com/nexssp/transport/tworker"
)

func TestTransportsImplementInterface(t *testing.T) {
	var _ transport.Transport = (*thttp.Transport)(nil)
	var _ transport.Transport = (*tcli.Transport)(nil)
	var _ transport.Transport = (*cron.Transport)(nil)
	var _ transport.Transport = (*tworker.Transport)(nil)
	var _ transport.Transport = (*tbus.Transport)(nil)
}
