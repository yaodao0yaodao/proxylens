include $(TOPDIR)/rules.mk

PKG_NAME:=proxylens
PKG_VERSION:=0.3.2
PKG_RELEASE:=1
PKG_LICENSE:=MIT

include $(INCLUDE_DIR)/package.mk
include $(INCLUDE_DIR)/golang-package.mk

GO_PKG:=github.com/yaodao0yaodao/proxylens
GO_PKG_BUILD_PKG:=$(GO_PKG)/cmd/proxylens
GO_PKG_LDFLAGS_X:=main.version=$(PKG_VERSION)

define Package/proxylens
  SECTION:=net
  CATEGORY:=Network
  SUBMENU:=Web Servers/Proxies
  TITLE:=Exit-aware proxy quality analyzer and sing-box publisher
  URL:=https://github.com/yaodao0yaodao/proxylens
  DEPENDS:=$(GO_ARCH_DEPENDS) +ca-bundle +luci-base
endef

define Package/proxylens/conffiles
/etc/config/proxylens
endef

define Package/proxylens/install
	$(call GoPackage/Package/Install/Bin,$(1))
	$(INSTALL_DIR) $(1)/etc/config $(1)/etc/init.d
	$(INSTALL_CONF) ./root/etc/config/proxylens $(1)/etc/config/proxylens
	$(INSTALL_BIN) ./root/etc/init.d/proxylens $(1)/etc/init.d/proxylens
	$(CP) ./root/usr $(1)/
	$(CP) ./root/www $(1)/
endef

$(eval $(call GoBinPackage,proxylens))
$(eval $(call BuildPackage,proxylens))
