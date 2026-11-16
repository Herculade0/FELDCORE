package setup

import (
	"strings"
	"time"

	"github.com/Herculade0/feldcore/internal/auth"
)

// Grant is a session created by the admin step. The HTTP layer sets the
// cookie. The token is never written into the wizard view.
type Grant struct {
	Token   string
	Expires time.Time
}

func (s *Service) beginTOTP(req Request) (secret, uri string, err error) {
	if s.accounts == nil {
		return "", "", &OpError{"Two-factor setup is not available."}
	}
	account := strings.TrimSpace(req.Username)
	if admin := s.st.Snapshot().Admin; admin != nil {
		account = admin.Username
		if err := s.accounts.CheckPassword(req.CurrentPassword); err != nil {
			return "", "", &InputError{"Enter the current password before changing two-factor."}
		}
	}
	if account == "" {
		account = "admin"
	}
	return s.accounts.BeginEnrolment(account)
}

func (s *Service) applyAdmin(req Request) (recovery []string, grant *Grant, err error) {
	if s.st.Snapshot().Admin != nil {
		return s.enrolExisting(req)
	}
	if s.accounts == nil {
		return nil, nil, &OpError{"The administrator account cannot be created."}
	}
	user := strings.TrimSpace(req.Username)
	if req.Password != req.Password2 {
		return nil, nil, &InputError{"The two passwords don't match."}
	}
	if err := auth.ValidateStrong(user, req.Password); err != nil {
		return nil, nil, err
	}
	if !req.SkipTOTP {
		if strings.TrimSpace(req.TOTPCode) == "" {
			return nil, nil, &InputError{"Set up an authenticator and enter a code, or skip two-factor for now."}
		}
		if err := s.accounts.VerifyPending(req.TOTPCode); err != nil {
			return nil, nil, err
		}
	} else {
		s.accounts.CancelEnrolment()
	}
	token, exp, err := s.accounts.SetupAndLogin(user, req.Password)
	if err != nil {
		return nil, nil, err
	}
	s.ConsumeCode()
	grant = &Grant{Token: token, Expires: exp}
	if req.SkipTOTP {
		return nil, grant, nil
	}
	recovery, err = s.accounts.ConfirmEnrolment(req.TOTPCode)
	if err != nil {
		return nil, grant, err
	}
	if err := s.accounts.EnableTOTP(); err != nil {
		return recovery, grant, err
	}
	return recovery, grant, nil
}

func (s *Service) enrolExisting(req Request) ([]string, *Grant, error) {
	if req.SkipTOTP || strings.TrimSpace(req.TOTPCode) == "" {
		return nil, nil, nil
	}
	if s.accounts == nil {
		return nil, nil, &OpError{"Two-factor setup is not available."}
	}
	if err := s.accounts.VerifyPending(req.TOTPCode); err != nil {
		return nil, nil, err
	}
	recovery, err := s.accounts.ConfirmEnrolment(req.TOTPCode)
	if err != nil {
		return nil, nil, err
	}
	if err := s.accounts.EnableTOTP(); err != nil {
		return nil, nil, err
	}
	return recovery, nil, nil
}

func (s *Service) applySignin(req Request) ([]string, error) {
	if s.accounts == nil {
		return nil, &OpError{"Two-factor setup is not available."}
	}
	if req.SkipTOTP && !req.DisableTOTP {
		return nil, nil
	}
	if err := s.accounts.CheckPassword(req.CurrentPassword); err != nil {
		return nil, &InputError{"Enter the current password before changing two-factor."}
	}
	if req.DisableTOTP {
		if err := s.accounts.DisableTOTP(req.CurrentPassword, req.TOTPCode); err != nil {
			return nil, err
		}
		return nil, nil
	}
	if strings.TrimSpace(req.TOTPCode) == "" {
		return nil, &InputError{"Enter the code from the authenticator, or skip this step."}
	}
	recovery, err := s.accounts.ConfirmEnrolment(req.TOTPCode)
	if err != nil {
		return nil, err
	}
	if err := s.accounts.EnableTOTP(); err != nil {
		return nil, err
	}
	return recovery, nil
}
