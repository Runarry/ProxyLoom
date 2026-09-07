package ir

// Each public structural type validates direct Go construction as well as its
// own JSON boundary. Aliases avoid recursively invoking UnmarshalJSON.
func (v ID) Validate() error                    { return validateValue(v, "uuid") }
func (v Endpoint) Validate() error              { return validateValue(v, "endpoint") }
func (v *MethodPasswordAuth) Validate() error   { return validateValue(v, "method_password_auth") }
func (v *VMessAuth) Validate() error            { return validateValue(v, "vmess_auth") }
func (v *UUIDAuth) Validate() error             { return validateValue(v, "uuid_auth") }
func (v *PasswordAuth) Validate() error         { return validateValue(v, "password_auth") }
func (v *UsernamePasswordAuth) Validate() error { return validateValue(v, "username_password_auth") }
func (v *NoAuth) Validate() error               { return validateValue(v, "no_auth") }
func (v *NativeTCPTransport) Validate() error   { return validateValue(v, "native_tcp") }
func (v *WebSocketTransport) Validate() error   { return validateValue(v, "websocket") }
func (v *NoSecurity) Validate() error           { return validateValue(v, "no_security") }
func (v *TLSSecurity) Validate() error          { return validateValue(v, "tls") }
func (v *RealitySecurity) Validate() error      { return validateValue(v, "reality") }
func (v Features) Validate() error              { return validateValue(v, "features") }
func (v Extensions) Validate() error            { return validateValue(v, "extensions") }
func (v Origin) Validate() error                { return validateValue(v, "origin") }

func (v *Endpoint) UnmarshalJSON(data []byte) error {
	type plain Endpoint
	var next plain
	if err := decodePlain(data, "endpoint", &next); err != nil {
		return err
	}
	*v = Endpoint(next)
	return nil
}
func (v *MethodPasswordAuth) UnmarshalJSON(data []byte) error {
	type plain MethodPasswordAuth
	var next plain
	if err := decodePlain(data, "method_password_auth", &next); err != nil {
		return err
	}
	*v = MethodPasswordAuth(next)
	return nil
}
func (v *VMessAuth) UnmarshalJSON(data []byte) error {
	type plain VMessAuth
	var next plain
	if err := decodePlain(data, "vmess_auth", &next); err != nil {
		return err
	}
	*v = VMessAuth(next)
	return nil
}
func (v *UUIDAuth) UnmarshalJSON(data []byte) error {
	type plain UUIDAuth
	var next plain
	if err := decodePlain(data, "uuid_auth", &next); err != nil {
		return err
	}
	*v = UUIDAuth(next)
	return nil
}
func (v *PasswordAuth) UnmarshalJSON(data []byte) error {
	type plain PasswordAuth
	var next plain
	if err := decodePlain(data, "password_auth", &next); err != nil {
		return err
	}
	*v = PasswordAuth(next)
	return nil
}
func (v *UsernamePasswordAuth) UnmarshalJSON(data []byte) error {
	type plain UsernamePasswordAuth
	var next plain
	if err := decodePlain(data, "username_password_auth", &next); err != nil {
		return err
	}
	*v = UsernamePasswordAuth(next)
	return nil
}
func (v *NoAuth) UnmarshalJSON(data []byte) error {
	type plain NoAuth
	var next plain
	if err := decodePlain(data, "no_auth", &next); err != nil {
		return err
	}
	*v = NoAuth(next)
	return nil
}
func (v *NativeTCPTransport) UnmarshalJSON(data []byte) error {
	type plain NativeTCPTransport
	var next plain
	if err := decodePlain(data, "native_tcp", &next); err != nil {
		return err
	}
	*v = NativeTCPTransport(next)
	return nil
}
func (v *WebSocketTransport) UnmarshalJSON(data []byte) error {
	type plain WebSocketTransport
	var next plain
	if err := decodePlain(data, "websocket", &next); err != nil {
		return err
	}
	*v = WebSocketTransport(next)
	return nil
}
func (v *NoSecurity) UnmarshalJSON(data []byte) error {
	type plain NoSecurity
	var next plain
	if err := decodePlain(data, "no_security", &next); err != nil {
		return err
	}
	*v = NoSecurity(next)
	return nil
}
func (v *TLSSecurity) UnmarshalJSON(data []byte) error {
	type plain TLSSecurity
	var next plain
	if err := decodePlain(data, "tls", &next); err != nil {
		return err
	}
	*v = TLSSecurity(next)
	return nil
}
func (v *RealitySecurity) UnmarshalJSON(data []byte) error {
	type plain RealitySecurity
	var next plain
	if err := decodePlain(data, "reality", &next); err != nil {
		return err
	}
	*v = RealitySecurity(next)
	return nil
}
func (v *Features) UnmarshalJSON(data []byte) error {
	type plain Features
	var next plain
	if err := decodePlain(data, "features", &next); err != nil {
		return err
	}
	*v = Features(next)
	return nil
}
func (v *Extensions) UnmarshalJSON(data []byte) error {
	type plain Extensions
	var next plain
	if err := decodePlain(data, "extensions", &next); err != nil {
		return err
	}
	*v = Extensions(next)
	return nil
}
func (v *Origin) UnmarshalJSON(data []byte) error {
	type plain Origin
	var next plain
	if err := decodePlain(data, "origin", &next); err != nil {
		return err
	}
	*v = Origin(next)
	return nil
}
