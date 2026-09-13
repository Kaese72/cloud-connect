// cloud-connect-server runs the chisel server that accepts the reverse
// tunnel from an enrolled appliance's cloud-connect-client - see the
// repo's README, "Architecture". It is linked in as a library rather than
// exec'd as the standalone chisel binary, matching cloud-connect-client's
// tunnel supervisor, so neither side of the tunnel depends on the chisel
// CLI being present in the image.
//
// It is a straight port of what start.sh used to invoke as
// `chisel server --port 8081 --auth "${CLOUD_CONNECT_AUTH}" --reverse`,
// including chisel CLI's own default host (0.0.0.0) and keepalive (25s).
package main

import (
	"log"
	"os"
	"time"

	chserver "github.com/jpillora/chisel/server"
)

func main() {
	auth := os.Getenv("CLOUD_CONNECT_AUTH")
	if auth == "" {
		log.Fatal("CLOUD_CONNECT_AUTH is required")
	}
	s, err := chserver.NewServer(&chserver.Config{
		Auth:      auth,
		Reverse:   true,
		KeepAlive: 25 * time.Second,
	})
	if err != nil {
		log.Fatal(err)
	}
	// Run blocks for the life of the process - if it returns, the container
	// exits and Kubernetes restarts the pod, same as when chisel was PID 1.
	if err := s.Run("0.0.0.0", "8081"); err != nil {
		log.Fatal(err)
	}
}
