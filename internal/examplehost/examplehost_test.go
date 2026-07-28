package examplehost_test

import (
	"context"
	"testing"

	"outband/internal/bmc"
	"outband/internal/config"
	_ "outband/internal/examplehost"
	"outband/internal/provider"
)

func TestRegisterAndNew(t *testing.T) {
	if !provider.Known("examplehost") {
		t.Fatal("examplehost should be registered via init")
	}

	client, err := provider.New(config.HostConfig{
		ID:       "ex1",
		Provider: "examplehost",
		Host:     "127.0.0.1",
		User:     "u",
		Password: "p",
		Options: config.OptionMap{
			"examplehost": []byte(`{"model":"lab-vm","powered_on":false}`),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	features := bmc.ClientFeatures(client)
	if !features.Has(bmc.FeatureIdentity) || !features.Has(bmc.FeaturePower) {
		t.Fatalf("features=%#x", features)
	}
	if features.Has(bmc.FeatureSensors) || features.Has(bmc.FeatureConsole) {
		t.Fatalf("must not advertise unsupported features: %#x", features)
	}

	info, err := client.MCInfo(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if info.Model != "lab-vm" {
		t.Fatalf("model=%q", info.Model)
	}

	st, err := client.PowerStatus(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if st.IsOn {
		t.Fatal("expected powered_on false from options")
	}

	if _, err := client.Sensors(context.Background()); err != bmc.ErrUnsupported {
		t.Fatalf("Sensors err=%v want ErrUnsupported", err)
	}
}
