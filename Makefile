BINARY      := proxmox-autoscaler
BIN_DIR     := bin
CMD_PATH    := ./cmd/autoscaler

INSTALL_BIN     := /usr/local/bin/$(BINARY)
INSTALL_CFG_DIR := /etc/proxmox-autoscaler
INSTALL_SVC     := /etc/systemd/system/$(BINARY).service

.PHONY: build build-linux install uninstall systemd-enable systemd-disable

build:
	go build -o $(BIN_DIR)/$(BINARY) $(CMD_PATH)

build-linux:
	GOOS=linux GOARCH=amd64 go build -o $(BIN_DIR)/$(BINARY)-linux-amd64 $(CMD_PATH)

install: build
	install -m 0755 $(BIN_DIR)/$(BINARY) $(INSTALL_BIN)
	install -d $(INSTALL_CFG_DIR)
	install -m 0644 configs/autoscaler.yaml $(INSTALL_CFG_DIR)/autoscaler.yaml
	install -m 0644 deployments/$(BINARY).service $(INSTALL_SVC)
	@echo "Installed $(BINARY) to $(INSTALL_BIN)"
	@echo "Config at $(INSTALL_CFG_DIR)/autoscaler.yaml — edit before starting the service"
	@echo "Reload systemd: systemctl daemon-reload"

uninstall:
	rm -f $(INSTALL_BIN)
	rm -f $(INSTALL_SVC)
	@echo "Removed binary and service unit. Config at $(INSTALL_CFG_DIR) was left intact."

systemd-enable:
	systemctl daemon-reload
	systemctl enable $(BINARY).service
	systemctl start $(BINARY).service

systemd-disable:
	systemctl stop $(BINARY).service
	systemctl disable $(BINARY).service
