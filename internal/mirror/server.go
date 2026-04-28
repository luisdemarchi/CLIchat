package mirror

type Status struct {
	Enabled bool   `json:"enabled"`
	Mode    string `json:"mode"`
	Address string `json:"address"`
	Note    string `json:"note"`
}

type Server struct {
	status Status
}

func NewServer() *Server {
	return &Server{
		status: Status{
			Enabled: false,
			Mode:    "local-disabled",
			Address: "",
			Note:    "Espelhamento via rede local/Tailscale ainda nao iniciado.",
		},
	}
}

func (s *Server) Status() Status {
	return s.status
}
