'use strict';
'require view';
'require form';
'require uci';

return view.extend({
	load: function() {
		return uci.load('proxylens');
	},

	render: function() {
		var m = new form.Map('proxylens', _('ProxyLens'),
			_('持续检测代理节点出口质量，并发布适用于 SFA 和 Carton 的 sing-box 配置。'));
		var s = m.section(form.NamedSection, 'main', 'proxylens', _('服务设置'));
		s.addremove = false;

		var o = s.option(form.Flag, 'enabled', _('启用服务'));
		o.rmempty = false;
		o = s.option(form.Value, 'port', _('Web 与订阅端口'));
		o.datatype = 'port';
		o.default = '9099';
		o.rmempty = false;
		o = s.option(form.Value, 'public_base_url', _('公网 / DDNS 基础地址'));
		o.placeholder = 'https://proxy.example.com:9099';
		o.description = _('留空时 Web 管理页使用当前路由器地址；发布到公网时建议使用 HTTPS 反向代理。');
		o = s.option(form.Value, 'database_path', _('数据库保存位置'));
		o.default = '/etc/proxylens/proxylens.db';
		o.rmempty = false;
		o.description = _('必须填写绝对路径。保存并应用后会先停止服务，再将当前数据库及 WAL 文件移动到新位置；若目标已有数据库则拒绝覆盖。');
		o = s.option(form.Value, 'admin_token', _('管理令牌'));
		o.password = true;
		o.readonly = true;
		o.description = _('服务首次启动时自动生成。Web 管理页需要此令牌。');

		o = s.option(form.Button, '_open', _('Web 管理页'));
		o.inputtitle = _('打开 ProxyLens');
		o.inputstyle = 'apply';
		o.onclick = function() {
			var port = uci.get('proxylens', 'main', 'port') || '9099';
			window.open(window.location.protocol + '//' + window.location.hostname + ':' + port + '/', '_blank', 'noopener');
		};
		return m.render();
	}
});
