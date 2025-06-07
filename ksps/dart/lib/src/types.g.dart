// GENERATED CODE - DO NOT MODIFY BY HAND

part of 'types.dart';

// **************************************************************************
// JsonSerializableGenerator
// **************************************************************************

WsMessage _$WsMessageFromJson(Map<String, dynamic> json) => WsMessage(
      action: json['action'] as String,
      topic: json['topic'] as String?,
      data: json['data'],
      from: json['from'] as String?,
      to: json['to'] as String?,
      ackId: json['ack_id'] as String?,
    );

Map<String, dynamic> _$WsMessageToJson(WsMessage instance) {
  final val = <String, dynamic>{
    'action': instance.action,
  };

  void writeNotNull(String key, dynamic value) {
    if (value != null) {
      val[key] = value;
    }
  }

  writeNotNull('topic', instance.topic);
  writeNotNull('data', instance.data);
  writeNotNull('from', instance.from);
  writeNotNull('to', instance.to);
  writeNotNull('ack_id', instance.ackId);
  return val;
}

AckResponse _$AckResponseFromJson(Map<String, dynamic> json) => AckResponse(
      ackId: json['ack_id'] as String,
      clientId: json['client_id'] as String,
      success: json['success'] as bool,
      error: json['error'] as String?,
    );

Map<String, dynamic> _$AckResponseToJson(AckResponse instance) {
  final val = <String, dynamic>{
    'ack_id': instance.ackId,
    'client_id': instance.clientId,
    'success': instance.success,
  };

  void writeNotNull(String key, dynamic value) {
    if (value != null) {
      val[key] = value;
    }
  }

  writeNotNull('error', instance.error);
  return val;
} 