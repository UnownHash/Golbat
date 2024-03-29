// Code generated by protoc-gen-go-grpc. DO NOT EDIT.
// versions:
// - protoc-gen-go-grpc v1.2.0
// - protoc             v3.21.12
// source: grpc/raw_receiver.proto

package grpc

import (
	context "context"
	grpc "google.golang.org/grpc"
	codes "google.golang.org/grpc/codes"
	status "google.golang.org/grpc/status"
)

// This is a compile-time assertion to ensure that this generated file
// is compatible with the grpc package it is being compiled against.
// Requires gRPC-Go v1.32.0 or later.
const _ = grpc.SupportPackageIsVersion7

// RawProtoClient is the client API for RawProto service.
//
// For semantics around ctx use and closing/ending streaming RPCs, please refer to https://pkg.go.dev/google.golang.org/grpc/?tab=doc#ClientConn.NewStream.
type RawProtoClient interface {
	SubmitRawProto(ctx context.Context, in *RawProtoRequest, opts ...grpc.CallOption) (*RawProtoResponse, error)
}

type rawProtoClient struct {
	cc grpc.ClientConnInterface
}

func NewRawProtoClient(cc grpc.ClientConnInterface) RawProtoClient {
	return &rawProtoClient{cc}
}

func (c *rawProtoClient) SubmitRawProto(ctx context.Context, in *RawProtoRequest, opts ...grpc.CallOption) (*RawProtoResponse, error) {
	out := new(RawProtoResponse)
	err := c.cc.Invoke(ctx, "/raw_receiver.RawProto/SubmitRawProto", in, out, opts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

// RawProtoServer is the server API for RawProto service.
// All implementations must embed UnimplementedRawProtoServer
// for forward compatibility
type RawProtoServer interface {
	SubmitRawProto(context.Context, *RawProtoRequest) (*RawProtoResponse, error)
	mustEmbedUnimplementedRawProtoServer()
}

// UnimplementedRawProtoServer must be embedded to have forward compatible implementations.
type UnimplementedRawProtoServer struct {
}

func (UnimplementedRawProtoServer) SubmitRawProto(context.Context, *RawProtoRequest) (*RawProtoResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "method SubmitRawProto not implemented")
}
func (UnimplementedRawProtoServer) mustEmbedUnimplementedRawProtoServer() {}

// UnsafeRawProtoServer may be embedded to opt out of forward compatibility for this service.
// Use of this interface is not recommended, as added methods to RawProtoServer will
// result in compilation errors.
type UnsafeRawProtoServer interface {
	mustEmbedUnimplementedRawProtoServer()
}

func RegisterRawProtoServer(s grpc.ServiceRegistrar, srv RawProtoServer) {
	s.RegisterService(&RawProto_ServiceDesc, srv)
}

func _RawProto_SubmitRawProto_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(RawProtoRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(RawProtoServer).SubmitRawProto(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: "/raw_receiver.RawProto/SubmitRawProto",
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(RawProtoServer).SubmitRawProto(ctx, req.(*RawProtoRequest))
	}
	return interceptor(ctx, in, info, handler)
}

// RawProto_ServiceDesc is the grpc.ServiceDesc for RawProto service.
// It's only intended for direct use with grpc.RegisterService,
// and not to be introspected or modified (even as a copy)
var RawProto_ServiceDesc = grpc.ServiceDesc{
	ServiceName: "raw_receiver.RawProto",
	HandlerType: (*RawProtoServer)(nil),
	Methods: []grpc.MethodDesc{
		{
			MethodName: "SubmitRawProto",
			Handler:    _RawProto_SubmitRawProto_Handler,
		},
	},
	Streams:  []grpc.StreamDesc{},
	Metadata: "grpc/raw_receiver.proto",
}
