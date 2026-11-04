package bookings

import (
	"context"
	"errors"
	"testing"
)

func TestBuyerFieldsPersistAndValidate(t *testing.T) {
	s, base := fixture(t)
	base.FirstName, base.LastName, base.Email = "María José", "Pérez Gómez", "buyer@example.com"
	base.PhoneAreaCode, base.Phone = "351", "+543511234567"
	r, _, err := s.Create(context.Background(), base)
	if err != nil {
		t.Fatal(err)
	}
	if r.FirstName != base.FirstName || r.LastName != base.LastName || r.Name != "María José Pérez Gómez" || r.PhoneAreaCode != "351" || r.Email != base.Email {
		t.Fatal("buyer data lost", r)
	}
	for _, mutate := range []func(*Input){
		func(i *Input) { i.Email = "" }, func(i *Input) { i.LastName = " " },
		func(i *Input) { i.PhoneAreaCode = "0351" }, func(i *Input) { i.PhoneAreaCode = "11" },
		func(i *Input) { i.Phone = "+54351123456" }, func(i *Input) { i.Email = "bad" },
	} {
		input := base
		mutate(&input)
		if err = s.Validate(input); !errors.Is(err, ErrInvalid) {
			t.Fatal("invalid buyer accepted", input, err)
		}
	}
	for _, phone := range []struct{ area, number string }{{"11", "+541112345678"}, {"2954", "+542954123456"}} {
		input := base
		input.PhoneAreaCode, input.Phone = phone.area, phone.number
		// Validate expects the full name already composed by Create.
		if err = s.Validate(input); err != nil {
			t.Fatal("valid area rejected", phone, err)
		}
	}
}
